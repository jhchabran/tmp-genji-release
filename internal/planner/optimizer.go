package planner

import (
	"github.com/jhchabran/tmp-genji-release/document"
	"github.com/jhchabran/tmp-genji-release/internal/database"
	"github.com/jhchabran/tmp-genji-release/internal/expr"
	"github.com/jhchabran/tmp-genji-release/internal/sql/scanner"
	"github.com/jhchabran/tmp-genji-release/internal/stream"
	"github.com/jhchabran/tmp-genji-release/internal/stringutil"
)

var optimizerRules = []func(s *stream.Stream, tx *database.Transaction, params []expr.Param) (*stream.Stream, error){
	SplitANDConditionRule,
	PrecalculateExprRule,
	RemoveUnnecessaryFilterNodesRule,
	RemoveUnnecessaryDistinctNodeRule,
	RemoveUnnecessaryProjection,
	UseIndexBasedOnFilterNodeRule,
}

// Optimize takes a tree, applies a list of optimization rules
// and returns an optimized tree.
// Depending on the rule, the tree may be modified in place or
// replaced by a new one.
func Optimize(s *stream.Stream, tx *database.Transaction, params []expr.Param) (*stream.Stream, error) {
	var err error

	for _, rule := range optimizerRules {
		s, err = rule(s, tx, params)
		if err != nil {
			return nil, err
		}
		if s.Op == nil {
			break
		}
	}

	return s, nil
}

// SplitANDConditionRule splits any filter node whose condition
// is one or more AND operators into one or more filter nodes.
// The condition won't be split if the expression tree contains an OR
// operation.
// Example:
//   this:
//     filter(a > 2 AND b != 3 AND c < 2)
//   becomes this:
//     filter(a > 2)
//     filter(b != 3)
//     filter(c < 2)
func SplitANDConditionRule(s *stream.Stream, _ *database.Transaction, _ []expr.Param) (*stream.Stream, error) {
	n := s.Op

	for n != nil {
		if f, ok := n.(*stream.FilterOperator); ok {
			cond := f.E
			if cond != nil {
				// The AND operator has one of the lowest precedence,
				// only OR has a lower precedence,
				// which means that if AND is used without OR, it will be at
				// the top of the expression tree.
				if op, ok := cond.(expr.Operator); ok && op.Token() == scanner.AND {
					exprs := splitANDExpr(cond)

					cur := n.GetPrev()
					s.Remove(n)

					for _, e := range exprs {
						cur = stream.InsertAfter(cur, stream.Filter(e))
					}

					if s.Op == nil {
						s.Op = cur
					}
				}
			}
		}

		n = n.GetPrev()
	}

	return s, nil
}

// splitANDExpr takes an expression and splits it by AND operator.
func splitANDExpr(cond expr.Expr) (exprs []expr.Expr) {
	op, ok := cond.(expr.Operator)
	if ok && op.Token() == scanner.AND {
		exprs = append(exprs, splitANDExpr(op.LeftHand())...)
		exprs = append(exprs, splitANDExpr(op.RightHand())...)
		return
	}

	exprs = append(exprs, cond)
	return
}

// PrecalculateExprRule evaluates any constant sub-expression that can be evaluated
// before running the query and replaces it by the result of the evaluation.
// The result of constant sub-expressions, like "3 + 4", is always the same and thus
// can be precalculated.
// Examples:
//   3 + 4 --> 7
//   3 + 1 > 10 - a --> 4 > 10 - a
func PrecalculateExprRule(s *stream.Stream, _ *database.Transaction, params []expr.Param) (*stream.Stream, error) {
	n := s.Op

	var err error
	for n != nil {
		switch t := n.(type) {
		case *stream.FilterOperator:
			t.E, err = precalculateExpr(t.E, params)
			if err != nil {
				return nil, err
			}
		case *stream.ProjectOperator:
			for i, e := range t.Exprs {
				t.Exprs[i], err = precalculateExpr(e, params)
				if err != nil {
					return nil, err
				}
			}
		}

		n = n.GetPrev()
	}

	return s, nil
}

// precalculateExpr is a recursive function that tries to precalculate
// expression nodes when possible.
// it returns a new expression with simplified nodes.
// if no simplification is possible it returns the same expression.
func precalculateExpr(e expr.Expr, params []expr.Param) (expr.Expr, error) {
	switch t := e.(type) {
	case expr.LiteralExprList:
		// we assume that the list of expressions contains only literals
		// until proven wrong.
		literalsOnly := true
		for i, te := range t {
			newExpr, err := precalculateExpr(te, params)
			if err != nil {
				return nil, err
			}
			if _, ok := newExpr.(expr.LiteralValue); !ok {
				literalsOnly = false
			}
			t[i] = newExpr
		}

		// if literalsOnly is still true, it means we have a list of constant expressions
		// (ex: [1, 4, true]). We can transform that into a document.Array.
		if literalsOnly {
			values := make([]document.Value, len(t))
			for i := range t {
				values[i] = document.Value(t[i].(expr.LiteralValue))
			}

			return expr.LiteralValue(document.NewArrayValue(document.NewValueBuffer(values...))), nil
		}

	case *expr.KVPairs:
		// we assume that the list of kvpairs contains only literals
		// until proven wrong.
		literalsOnly := true

		var err error
		for i, kv := range t.Pairs {
			kv.V, err = precalculateExpr(kv.V, params)
			if err != nil {
				return nil, err
			}
			if _, ok := kv.V.(expr.LiteralValue); !ok {
				literalsOnly = false
			}
			t.Pairs[i] = kv
		}

		// if literalsOnly is still true, it means we have a list of kvpairs
		// that only contain constant values (ex: {"a": 1, "b": true}.
		// We can transform that into a document.Document.
		if literalsOnly {
			var fb document.FieldBuffer
			for i := range t.Pairs {
				fb.Add(t.Pairs[i].K, document.Value(t.Pairs[i].V.(expr.LiteralValue)))
			}

			return expr.LiteralValue(document.NewDocumentValue(&fb)), nil
		}
	case expr.Operator:
		// since expr.Operator is an interface,
		// this optimization must only be applied to
		// a few selected operators that we know about.
		tok := t.Token()
		if tok != scanner.AND &&
			tok != scanner.OR &&
			!expr.IsArithmeticOperator(t) &&
			!expr.IsComparisonOperator(t) {
			return e, nil
		}

		lh, err := precalculateExpr(t.LeftHand(), params)
		if err != nil {
			return nil, err
		}
		rh, err := precalculateExpr(t.RightHand(), params)
		if err != nil {
			return nil, err
		}
		t.SetLeftHandExpr(lh)
		t.SetRightHandExpr(rh)

		_, leftIsLit := lh.(expr.LiteralValue)
		_, rightIsLit := rh.(expr.LiteralValue)
		// if both operands are literals, we can precalculate them now
		if leftIsLit && rightIsLit {
			v, err := t.Eval(&expr.Environment{})
			// any error encountered here is unexpected
			if err != nil {
				panic(err)
			}
			// we replace this expression with the result of its evaluation
			return expr.LiteralValue(v), nil
		}
	case expr.PositionalParam, expr.NamedParam:
		v, err := e.Eval(&expr.Environment{Params: params})
		if err != nil {
			return nil, err
		}
		return expr.LiteralValue(v), nil
	}

	return e, nil
}

// RemoveUnnecessaryFilterNodesRule removes any filter node whose
// condition is a constant expression that evaluates to a truthy value.
// if it evaluates to a falsy value, it considers that the tree
// will not stream any document, so it returns an empty tree.
func RemoveUnnecessaryFilterNodesRule(s *stream.Stream, _ *database.Transaction, _ []expr.Param) (*stream.Stream, error) {
	n := s.Op

	for n != nil {
		if f, ok := n.(*stream.FilterOperator); ok {
			if f.E != nil {
				switch t := f.E.(type) {
				case expr.LiteralValue:
					// Constant expression
					// ex: WHERE 1

					// if the expr is falsy, we return an empty tree
					ok, err := document.Value(t).IsTruthy()
					if err != nil {
						return nil, err
					}
					if !ok {
						return &stream.Stream{}, nil
					}

					// if the expr is truthy, we remove the node from the stream
					prev := n.GetPrev()
					s.Remove(n)
					n = prev
					continue
				case *expr.InOperator:
					// IN operator with empty array
					// ex: WHERE a IN []
					lv, ok := t.RightHand().(expr.LiteralValue)
					if ok && lv.Type == document.ArrayValue {
						l, err := document.ArrayLength(lv.V.(document.Array))
						if err != nil {
							return nil, err
						}
						// if the array is empty, we return an empty stream
						if l == 0 {
							return &stream.Stream{}, nil
						}
					}
				}
			}
		}

		n = n.GetPrev()
	}

	return s, nil
}

// RemoveUnnecessaryProjection removes any project node whose
// expression is a wildcard only.
func RemoveUnnecessaryProjection(s *stream.Stream, _ *database.Transaction, _ []expr.Param) (*stream.Stream, error) {
	n := s.Op

	for n != nil {
		if p, ok := n.(*stream.ProjectOperator); ok {
			if len(p.Exprs) == 1 {
				if _, ok := p.Exprs[0].(expr.Wildcard); ok {
					prev := n.GetPrev()
					s.Remove(n)
					n = prev
				}
			}
		}

		n = n.GetPrev()
	}

	return s, nil
}

// RemoveUnnecessaryDistinctNodeRule removes any Dedup nodes
// where projection is already unique.
func RemoveUnnecessaryDistinctNodeRule(s *stream.Stream, tx *database.Transaction, _ []expr.Param) (*stream.Stream, error) {
	n := s.Op

	// we assume that if we are reading from a table, the first
	// operator of the stream has to be a SeqScanOperator
	firstNode := s.First()
	if firstNode == nil {
		return s, nil
	}
	st, ok := firstNode.(*stream.SeqScanOperator)
	if !ok {
		return s, nil
	}

	t, err := tx.Catalog.GetTable(tx, st.TableName)
	if err != nil {
		return nil, err
	}

	// this optimization applies to project operators that immediately follow distinct
	for n != nil {
		if d, ok := n.(*stream.DistinctOperator); ok {
			prev := d.GetPrev()
			if prev != nil {
				pn, ok := prev.(*stream.ProjectOperator)
				if ok {

					// if the projection is unique, we remove the node from the tree
					if isProjectionUnique(t.Indexes, pn, t.Info.GetPrimaryKey()) {
						s.Remove(n)
						n = prev
						continue
					}
				}
			}
		}

		n = n.GetPrev()
	}

	return s, nil
}

func isProjectionUnique(indexes database.Indexes, po *stream.ProjectOperator, pk *database.FieldConstraint) bool {
	for _, field := range po.Exprs {
		e, ok := field.(*expr.NamedExpr)
		if ok {
			field = e.Expr
			return false
		}

		switch v := field.(type) {
		case expr.Path:
			if pk != nil && pk.Path.IsEqual(document.Path(v)) {
				continue
			}

			if idx := indexes.GetIndexByPath(document.Path(v)); idx != nil && idx.Info.Unique {
				continue
			}
		case *expr.PKFunc:
			continue
		}

		return false // if one field is not unique, so projection is not unique too.
	}

	return true
}

type filterNode struct {
	path document.Path
	v    document.Value
	f    *stream.FilterOperator
}

// UseIndexBasedOnFilterNodeRule scans the tree for filter nodes whose conditions are
// operators that satisfies the following criterias:
// - is a comparison operator
// - one of its operands is a path expression that is indexed
// - the other operand is a literal value or a parameter
//
// If one or many are found, it will replace the input node by an indexInputNode using this index,
// removing the now irrelevant filter nodes.
//
// TODO(asdine): add support for ORDER BY
// TODO(jh): clarify cost code in composite indexes case
func UseIndexBasedOnFilterNodeRule(s *stream.Stream, tx *database.Transaction, params []expr.Param) (*stream.Stream, error) {
	// first we lookup for the seq scan node.
	// Here we will assume that at this point
	// if there is one it has to be the
	// first node of the stream.
	firstNode := s.First()
	if firstNode == nil {
		return s, nil
	}
	st, ok := firstNode.(*stream.SeqScanOperator)
	if !ok {
		return s, nil
	}
	t, err := tx.Catalog.GetTable(tx, st.TableName)
	if err != nil {
		return nil, err
	}

	var candidates []*candidate
	var filterNodes []filterNode

	// then we collect all usable filter nodes, in order to see what index (or PK) can be
	// used to replace them.
	for n := s.Op; n != nil; n = n.GetPrev() {
		if f, ok := n.(*stream.FilterOperator); ok {
			if f.E == nil {
				continue
			}

			op, ok := f.E.(expr.Operator)
			if !ok {
				continue
			}

			if !expr.OperatorIsIndexCompatible(op) {
				continue
			}

			// determine if the operator could benefit from an index
			ok, path, e := operatorCanUseIndex(op)
			if !ok {
				continue
			}

			ev, ok := e.(expr.LiteralValue)
			if !ok {
				continue
			}

			v := document.Value(ev)

			filterNodes = append(filterNodes, filterNode{path: path, v: v, f: f})

			// check for primary keys scan while iterating on the filter nodes
			if pk := t.Info.GetPrimaryKey(); pk != nil && pk.Path.IsEqual(path) {
				// if both types are different, don't select this scanner
				v, ok, err := operandCanUseIndex(pk.Type, pk.Path, t.Info.FieldConstraints, v)
				if err != nil {
					return nil, err
				}

				if !ok {
					continue
				} else {
					cd := candidate{
						filterOps: []*stream.FilterOperator{f},
						isPk:      true,
						priority:  3,
					}

					ranges, err := getRangesFromOp(op, v)
					if err != nil {
						return nil, err
					}

					cd.newOp = stream.PkScan(st.TableName, ranges...)
					cd.cost = ranges.Cost()

					candidates = append(candidates, &cd)
				}
			}
		}
	}

	findByPath := func(path document.Path) *filterNode {
		for _, fno := range filterNodes {
			if fno.path.IsEqual(path) {
				return &fno
			}
		}

		return nil
	}

	isNodeEq := func(fno *filterNode) bool {
		op := fno.f.E.(expr.Operator)
		return op.Token() == scanner.EQ || op.Token() == scanner.IN
	}
	isNodeComp := func(fno *filterNode) bool {
		op := fno.f.E.(expr.Operator)
		return expr.IsComparisonOperator(op)
	}

	// iterate on all indexes for that table, checking for each of them if its paths are matching
	// the filter nodes of the given query. The resulting nodes are ordered like the index paths.
outer:
	for _, idx := range t.Indexes {
		// order filter nodes by how the index paths order them; if absent, nil in still inserted
		found := make([]*filterNode, len(idx.Info.Paths))
		for i, path := range idx.Info.Paths {
			fno := findByPath(path)

			if fno != nil {
				// mark this path from the index as found
				found[i] = fno
			}
		}

		// Iterate on all the nodes for the given index, checking for each of its path, their is a corresponding node.
		// It's possible for an index to be selected if not all of its paths are covered by the nodes, if and only if
		// those are contiguous, relatively to the paths, i.e:
		//   - given idx_foo_abc(a, b, c)
		//   - given a query SELECT ... WHERE a = 1 AND b > 2
		//     - the paths a and b are contiguous in the index definition, this index can be used
		//   - given a query SELECT ... WHERE a = 1 AND c > 2
		//     - the paths a and c are not contiguous in the index definition, this index cannot be used for both values
		//       but it will be used with a and c with a normal filter node.
		var fops []*stream.FilterOperator
		var usableFilterNodes []*filterNode
		contiguous := true
		for i, fno := range found {
			if contiguous {
				if fno == nil {
					contiguous = false
					continue
				}

				// is looking ahead at the next node possible?
				if i+1 < len(found) {
					// is there another node found after this one?
					if found[i+1] != nil {
						// current one must be an eq node then
						if !isNodeEq(fno) {
							continue outer
						}
					} else {
						// the next node is the last one found, so the current one can also be a comparison and not just eq
						if !isNodeComp(fno) {
							continue outer
						}
					}
				} else {
					// that's the last filter node, it can be a comparison,
					if !isNodeComp(fno) {
						continue outer
					}
				}

				// what the index says this node type must be
				typ := idx.Info.Types[i]

				fno.v, ok, err = operandCanUseIndex(typ, fno.path, t.Info.FieldConstraints, fno.v)
				if err != nil {
					return nil, err
				}
				if !ok {
					continue outer
				}
			} else {
				// if on the index idx_abc(a,b,c), a is found, b isn't but c is
				// then idx_abc is valid but just with a, c will use a filter node instead
				continue
			}

			usableFilterNodes = append(usableFilterNodes, fno)
			fops = append(fops, fno.f)
		}

		// no nodes for the index has been found
		if found[0] == nil {
			continue outer
		}

		cd := candidate{
			filterOps: fops,
			isIndex:   true,
		}

		// there are probably less values to iterate on if the index is unique
		if idx.Info.Unique {
			cd.priority = 2
		} else {
			cd.priority = 1
		}

		ranges, err := getRangesFromFilterNodes(usableFilterNodes)
		if err != nil {
			return nil, err
		}

		cd.newOp = stream.IndexScan(idx.Info.IndexName, ranges...)
		cd.cost = ranges.Cost()

		candidates = append(candidates, &cd)
	}

	// determine which index is the most interesting and replace it in the tree.
	// we will assume that unique indexes are more interesting than list indexes
	// because they usually have less elements.
	var selectedCandidate *candidate
	var cost int

	for i, candidate := range candidates {
		currentCost := candidate.cost

		if selectedCandidate == nil {
			selectedCandidate = candidates[i]
			cost = currentCost
			continue
		}

		// With the current cost be computing on ranges, it's a bit hard to know what's best in
		// between indexes. So, before looking at the cost, we look at how many filter ops would
		// be replaced.
		if len(selectedCandidate.filterOps) < len(candidate.filterOps) {
			selectedCandidate = candidates[i]
			cost = currentCost
			continue
		} else if len(selectedCandidate.filterOps) == len(candidate.filterOps) {
			if currentCost < cost {
				selectedCandidate = candidates[i]
				cost = currentCost
				continue
			}

			// if the cost is the same and the candidate's related index has a higher priority,
			// select it.
			if currentCost == cost {
				if selectedCandidate.priority < candidate.priority {
					selectedCandidate = candidates[i]
				}
			}
		}
	}

	if selectedCandidate == nil {
		return s, nil
	}

	// remove the selection node from the tree
	for _, f := range selectedCandidate.filterOps {
		s.Remove(f)
	}

	// we replace the seq scan node by the selected index scan node
	stream.InsertBefore(s.First(), selectedCandidate.newOp)

	s.Remove(s.First().GetNext())

	return s, nil
}

type candidate struct {
	// filter operators to remove and replace by either an indexScan
	// or pkScan operators.
	filterOps []*stream.FilterOperator
	// the candidate indexScan or pkScan operator
	newOp stream.Operator
	// the cost of the candidate
	cost int
	// is this candidate reading from an index
	isIndex bool
	// is this candidate reading primary key ranges
	isPk bool
	// if the costs of two candidates are equal,
	// this number determines which node will be prioritized
	priority int
}

func operatorCanUseIndex(op expr.Operator) (bool, document.Path, expr.Expr) {
	lf, leftIsField := op.LeftHand().(expr.Path)
	rf, rightIsField := op.RightHand().(expr.Path)

	// Special case for IN operator: only left operand is valid for index usage
	// valid:   a IN [1, 2, 3]
	// invalid: 1 IN a
	if op.Token() == scanner.IN {
		if leftIsField && !rightIsField {
			rh := op.RightHand()
			// The IN operator can use indexes only if the right hand side is an array with constants.
			// At this point, we know that PrecalculateExprRule has converted any constant expression into
			// actual values, so we can check if the right hand side is an array.
			lv, ok := rh.(expr.LiteralValue)
			if !ok || lv.Type != document.ArrayValue {
				return false, nil, nil
			}

			return true, document.Path(lf), rh
		}

		return false, nil, nil
	}

	// path OP expr
	if leftIsField && !rightIsField {
		return true, document.Path(lf), op.RightHand()
	}

	// expr OP path
	if rightIsField && !leftIsField {
		return true, document.Path(rf), op.LeftHand()
	}

	return false, nil, nil
}

func operandCanUseIndex(indexType document.ValueType, path document.Path, fc database.FieldConstraints, v document.Value) (document.Value, bool, error) {
	// ensure the operand satisfies all the constraints, index can work only on exact types.
	// if a number is encountered, try to convert it to the right type if and only if the conversion
	// is lossless.
	converted, err := fc.ConvertValueAtPath(path, v, database.LosslessNumbersConversion)
	if err != nil {
		return v, false, err
	}

	// if the index is not typed, any operand can work
	if indexType.IsAny() {
		return converted, true, nil
	}

	// if the index is typed, it must be of the same type as the converted value
	return converted, indexType == converted.Type, nil
}

func getRangesFromFilterNodes(fnodes []*filterNode) (stream.IndexRanges, error) {
	var ranges stream.IndexRanges
	vb := document.NewValueBuffer()
	// store IN operands with their position (in the index paths) as a key
	inOperands := make(map[int]document.Array)

	for i, fno := range fnodes {
		op := fno.f.E.(expr.Operator)
		v := fno.v

		switch {
		case op.Token() == scanner.IN:
			// mark where the IN operator values are supposed to go is in the buffer
			// and what are the value needed to generate the ranges.
			// operatorCanUseIndex made sure v is an array.
			inOperands[i] = v.V.(document.Array)

			// placeholder for when we'll explode the IN operands in multiple ranges
			vb = vb.Append(document.Value{})
		case expr.IsComparisonOperator(op):
			vb = vb.Append(v)
		default:
			panic(stringutil.Sprintf("unknown operator %#v", op))
		}
	}

	if len(inOperands) > 1 {
		// TODO FEATURE https://github.com/jhchabran/tmp-genji-release/issues/392
		panic("unsupported operation: multiple IN operators on a composite index")
	}

	// a small helper func to create a range based on an operator type
	buildRange := func(op expr.Operator, vb *document.ValueBuffer) stream.IndexRange {
		var rng stream.IndexRange

		switch op.Token() {
		case scanner.EQ, scanner.IN:
			rng.Exact = true
			rng.Min = vb
		case scanner.GT:
			rng.Exclusive = true
			rng.Min = vb
		case scanner.GTE:
			rng.Min = vb
		case scanner.LT:
			rng.Exclusive = true
			rng.Max = vb
		case scanner.LTE:
			rng.Max = vb
		}

		return rng
	}

	// explode the IN operator values in multiple ranges
	for pos, operands := range inOperands {
		err := operands.Iterate(func(j int, value document.Value) error {
			newVB := document.NewValueBuffer()
			err := newVB.Copy(vb)
			if err != nil {
				return err
			}

			// insert IN operand at the right position, replacing the placeholder value
			newVB.Values[pos] = value

			// the last node is the only one that can be a comparison operator, so
			// it's the one setting the range behaviour
			last := fnodes[len(fnodes)-1]
			op := last.f.E.(expr.Operator)

			rng := buildRange(op, newVB)

			ranges = ranges.Append(rng)
			return nil
		})

		if err != nil {
			return nil, err
		}
	}

	// Were there any IN operators requiring multiple ranges?
	// If yes, we're done here.
	if len(ranges) > 0 {
		return ranges, nil
	}

	// the last node is the only one that can be a comparison operator, so
	// it's the one setting the range behaviour
	last := fnodes[len(fnodes)-1]
	op := last.f.E.(expr.Operator)
	rng := buildRange(op, vb)

	return stream.IndexRanges{rng}, nil
}

func getRangesFromOp(op expr.Operator, v document.Value) (stream.ValueRanges, error) {
	var ranges stream.ValueRanges

	switch op.Token() {
	case scanner.EQ:
		ranges = ranges.Append(stream.ValueRange{
			Min:   v,
			Exact: true,
		})
	case scanner.GT:
		ranges = ranges.Append(stream.ValueRange{
			Min:       v,
			Exclusive: true,
		})
	case scanner.GTE:
		ranges = ranges.Append(stream.ValueRange{
			Min: v,
		})
	case scanner.LT:
		ranges = ranges.Append(stream.ValueRange{
			Max:       v,
			Exclusive: true,
		})
	case scanner.LTE:
		ranges = ranges.Append(stream.ValueRange{
			Max: v,
		})
	case scanner.IN:
		// operatorCanUseIndex made sure e is an array.
		a := v.V.(document.Array)
		err := a.Iterate(func(i int, value document.Value) error {
			ranges = ranges.Append(stream.ValueRange{
				Min:   value,
				Exact: true,
			})
			return nil
		})
		if err != nil {
			return nil, err
		}
	default:
		panic(stringutil.Sprintf("unknown operator %#v", op))
	}

	return ranges, nil
}

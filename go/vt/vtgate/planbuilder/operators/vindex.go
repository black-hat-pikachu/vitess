/*
Copyright 2022 The Vitess Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package operators

import (
	"vitess.io/vitess/go/slices2"
	"vitess.io/vitess/go/vt/sqlparser"
	"vitess.io/vitess/go/vt/vterrors"
	"vitess.io/vitess/go/vt/vtgate/engine"
	"vitess.io/vitess/go/vt/vtgate/planbuilder/operators/ops"
	"vitess.io/vitess/go/vt/vtgate/planbuilder/plancontext"
	"vitess.io/vitess/go/vt/vtgate/semantics"
	"vitess.io/vitess/go/vt/vtgate/vindexes"
)

type (
	Vindex struct {
		OpCode  engine.VindexOpcode
		Table   VindexTable
		Vindex  vindexes.Vindex
		Solved  semantics.TableSet
		Columns []*sqlparser.ColName
		Value   sqlparser.Expr

		noInputs
	}

	// VindexTable contains information about the vindex table we want to query
	VindexTable struct {
		TableID    semantics.TableSet
		Alias      *sqlparser.AliasedTableExpr
		Table      sqlparser.TableName
		Predicates []sqlparser.Expr
		VTable     *vindexes.Table
	}
)

const VindexUnsupported = "WHERE clause for vindex function must be of the form id = <val> or id in(<val>,...)"

// Introduces implements the Operator interface
func (v *Vindex) introducesTableID() semantics.TableSet {
	return v.Solved
}

// Clone implements the Operator interface
func (v *Vindex) Clone([]ops.Operator) ops.Operator {
	clone := *v
	return &clone
}

func (v *Vindex) AddColumn(ctx *plancontext.PlanningContext, expr *sqlparser.AliasedExpr, _, addToGroupBy bool) (ops.Operator, int, error) {
	if addToGroupBy {
		return nil, 0, vterrors.VT13001("tried to add group by to a table")
	}

	offset, err := addColumn(ctx, v, expr.Expr)
	if err != nil {
		return nil, 0, err
	}

	return v, offset, nil
}

func colNameToExpr(c *sqlparser.ColName) *sqlparser.AliasedExpr {
	return &sqlparser.AliasedExpr{
		Expr: c,
		As:   sqlparser.IdentifierCI{},
	}
}

func (v *Vindex) GetColumns() ([]*sqlparser.AliasedExpr, error) {
	return slices2.Map(v.Columns, colNameToExpr), nil
}

func (v *Vindex) GetSelectExprs() (sqlparser.SelectExprs, error) {
	return transformColumnsToSelectExprs(v)
}

func (v *Vindex) GetOrdering() ([]ops.OrderBy, error) {
	return nil, nil
}

func (v *Vindex) GetColNames() []*sqlparser.ColName {
	return v.Columns
}
func (v *Vindex) AddCol(col *sqlparser.ColName) {
	v.Columns = append(v.Columns, col)
}

func (v *Vindex) CheckValid() error {
	if len(v.Table.Predicates) == 0 {
		return vterrors.VT12001(VindexUnsupported + " (where clause missing)")
	}

	return nil
}

func (v *Vindex) AddPredicate(ctx *plancontext.PlanningContext, expr sqlparser.Expr) (ops.Operator, error) {
	for _, e := range sqlparser.SplitAndExpression(nil, expr) {
		deps := ctx.SemTable.RecursiveDeps(e)
		if deps.NumberOfTables() > 1 {
			return nil, vterrors.VT12001(VindexUnsupported + " (multiple tables involved)")
		}
		// check if we already have a predicate
		if v.OpCode != engine.VindexNone {
			return nil, vterrors.VT12001(VindexUnsupported + " (multiple filters)")
		}

		// check LHS
		comparison, ok := e.(*sqlparser.ComparisonExpr)
		if !ok {
			return nil, vterrors.VT12001(VindexUnsupported + " (not a comparison)")
		}
		if comparison.Operator != sqlparser.EqualOp && comparison.Operator != sqlparser.InOp {
			return nil, vterrors.VT12001(VindexUnsupported + " (not equality)")
		}
		colname, ok := comparison.Left.(*sqlparser.ColName)
		if !ok {
			return nil, vterrors.VT12001(VindexUnsupported + " (lhs is not a column)")
		}
		if !colname.Name.EqualString("id") {
			return nil, vterrors.VT12001(VindexUnsupported + " (lhs is not id)")
		}

		// check RHS
		var err error
		if sqlparser.IsValue(comparison.Right) || sqlparser.IsSimpleTuple(comparison.Right) {
			v.Value = comparison.Right
		} else {
			return nil, vterrors.VT12001(VindexUnsupported + " (rhs is not a value)")
		}
		if err != nil {
			return nil, vterrors.VT12001(VindexUnsupported+": %v", err)
		}
		v.OpCode = engine.VindexMap
		v.Table.Predicates = append(v.Table.Predicates, e)
	}
	return v, nil
}

// TablesUsed implements the Operator interface.
// It is not keyspace-qualified.
func (v *Vindex) TablesUsed() []string {
	return []string{v.Table.Table.Name.String()}
}

func (v *Vindex) ShortDescription() string {
	return v.Vindex.String()
}

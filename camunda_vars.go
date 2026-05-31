package camunda

import (
	"time"

	"github.com/nativebpm/camunda/internal/vars"
)

type Variable = vars.Variable

func StringVariable(value string) Variable  { return vars.StringVariable(value) }
func IntVariable(value int) Variable        { return vars.IntVariable(value) }
func LongVariable(value int) Variable       { return vars.LongVariable(value) }
func DoubleVariable(value float64) Variable { return vars.DoubleVariable(value) }
func BooleanVariable(value bool) Variable   { return vars.BooleanVariable(value) }
func DateVariable(value time.Time) Variable { return vars.DateVariable(value) }
func JSONVariable(value any) Variable       { return vars.JSONVariable(value) }
func ListVariable(value any) Variable       { return vars.ListVariable(value) }
func NullVariable() Variable                { return vars.NullVariable() }

type Variables = vars.Variables

func NewVariables() *Variables {
	return vars.NewVariables()
}

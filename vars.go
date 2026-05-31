package camunda

import (
	"encoding/json"
	"fmt"
	"time"
)

// Variable represents a Camunda variable with type safety
type Variable struct {
	Value     any    `json:"value"`
	Type      string `json:"type"`
	ValueInfo any    `json:"valueInfo,omitempty"`
}

func StringVariable(value string) Variable {
	return Variable{Value: value, Type: "String"}
}

func IntVariable(value int) Variable {
	return Variable{Value: value, Type: "Integer"}
}

func LongVariable(value int) Variable {
	return Variable{Value: value, Type: "Long"}
}

func DoubleVariable(value float64) Variable {
	return Variable{Value: value, Type: "Double"}
}

func BooleanVariable(value bool) Variable {
	return Variable{Value: value, Type: "Boolean"}
}

func DateVariable(value time.Time) Variable {
	// Camunda expects dates in the format yyyy-MM-dd'T'HH:mm:ss.SSSZ
	// (note: timezone offset without colon, e.g. +0500). time.RFC3339
	// uses an offset with a colon (e.g. +05:00) which Camunda's
	// date parser may reject. Use a layout that produces the
	// expected format and include valueInfo.dateFormat so the
	// engine can correctly deserialize the value to java.util.Date.
	formatted := value.Format("2006-01-02T15:04:05.000-0700")
	return Variable{Value: formatted, Type: "Date",
		ValueInfo: map[string]any{"dateFormat": "yyyy-MM-dd'T'HH:mm:ss.SSSZ"}}
}

func JSONVariable(value any) Variable {
	jsonBytes, err := json.Marshal(value)
	if err != nil {
		return Variable{Value: fmt.Sprintf("ERROR: failed to marshal JSON: %v", err), Type: "String"}
	}
	return Variable{Value: string(jsonBytes), Type: "Object",
		ValueInfo: map[string]any{"objectTypeName": "java.util.LinkedHashMap", "serializationDataFormat": "application/json"}}
}

func ListVariable(value any) Variable {
	jsonBytes, err := json.Marshal(value)
	if err != nil {
		return Variable{Value: fmt.Sprintf("ERROR: failed to marshal list: %v", err), Type: "String"}
	}
	return Variable{Value: string(jsonBytes), Type: "Object",
		ValueInfo: map[string]any{"objectTypeName": "java.util.ArrayList", "serializationDataFormat": "application/json"}}
}

func NullVariable() Variable {
	return Variable{Value: nil, Type: "Null"}
}

// Variables is a convenience builder for creating a map[string]Variable
// for use when starting process instances or completing tasks.
type Variables struct {
	vars map[string]Variable
}

// NewVariables creates a new Variables builder.
func NewVariables() *Variables {
	return &Variables{vars: make(map[string]Variable)}
}

func (b *Variables) String(name, value string) *Variables {
	b.vars[name] = StringVariable(value)
	return b
}

func (b *Variables) Int(name string, value int) *Variables {
	b.vars[name] = IntVariable(value)
	return b
}

func (b *Variables) Long(name string, value int) *Variables {
	b.vars[name] = LongVariable(value)
	return b
}

func (b *Variables) Double(name string, value float64) *Variables {
	b.vars[name] = DoubleVariable(value)
	return b
}

func (b *Variables) Boolean(name string, value bool) *Variables {
	b.vars[name] = BooleanVariable(value)
	return b
}

func (b *Variables) Date(name string, value time.Time) *Variables {
	b.vars[name] = DateVariable(value)
	return b
}

func (b *Variables) JSON(name string, value any) *Variables {
	b.vars[name] = JSONVariable(value)
	return b
}

func (b *Variables) List(name string, value any) *Variables {
	b.vars[name] = ListVariable(value)
	return b
}

func (b *Variables) Null(name string) *Variables {
	b.vars[name] = NullVariable()
	return b
}

// Variables returns the underlying map[string]Variable
func (b *Variables) Variables() map[string]Variable {
	return b.vars
}

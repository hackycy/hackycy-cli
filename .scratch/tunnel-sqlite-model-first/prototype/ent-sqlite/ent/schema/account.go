package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
)

// Account is a small stand-in for an owner record in the Server model.
type Account struct {
	ent.Schema
}

func (Account) Fields() []ent.Field {
	return []ent.Field{
		field.String("name").Unique(),
	}
}

func (Account) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("allocations", Allocation.Type),
	}
}

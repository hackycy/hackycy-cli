package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// Allocation stands in for a resource whose active port must be unique per owner.
type Allocation struct {
	ent.Schema
}

func (Allocation) Fields() []ent.Field {
	return []ent.Field{
		field.Int("owner_id"),
		field.Int("port"),
		field.Bool("enabled").Default(true),
	}
}

func (Allocation) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("owner", Account.Type).
			Ref("allocations").
			Field("owner_id").
			Unique().
			Required(),
	}
}

func (Allocation) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("owner_id", "port").
			Unique().
			Annotations(entsql.IndexWhere("enabled = 1")),
	}
}

func (Allocation) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Checks: map[string]string{
			"port_range": "port BETWEEN 1 AND 65535",
		}},
	}
}

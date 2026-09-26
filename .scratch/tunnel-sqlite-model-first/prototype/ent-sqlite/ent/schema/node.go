package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
)

// Node is a minimal stand-in for the durable Node identity and runtime row.
type Node struct {
	ent.Schema
}

func (Node) Fields() []ent.Field {
	return []ent.Field{
		field.String("node_id").Unique(),
		field.String("controller_public").Optional(),
		field.Int64("highest_revision").Default(0),
		field.String("phase").Default("idle"),
	}
}

func (Node) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Checks: map[string]string{
			"revision_nonnegative": "highest_revision >= 0",
		}},
	}
}

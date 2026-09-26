package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
)

type Identity struct{ ent.Schema }

func (Identity) Fields() []ent.Field {
	return []ent.Field{
		field.Int("id"),
		field.String("node_id"),
		field.Bytes("private_key"),
		field.Bytes("public_key"),
	}
}

func (Identity) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "identity", Checks: map[string]string{
		"identity_single_row":         "id = 1",
		"identity_node_id_length":     "length(node_id) = 32",
		"identity_private_key_length": "length(private_key) = 32",
		"identity_public_key_length":  "length(public_key) = 32",
	}}}
}

type ControllerBinding struct{ ent.Schema }

func (ControllerBinding) Fields() []ent.Field {
	return []ent.Field{field.Int("id"), field.Bytes("controller_public")}
}

func (ControllerBinding) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "controller_binding", Checks: map[string]string{
		"binding_single_row":        "id = 1",
		"binding_public_key_length": "length(controller_public) = 32",
	}}}
}

type RuntimeState struct{ ent.Schema }

func (RuntimeState) Fields() []ent.Field {
	return []ent.Field{
		field.Int("id"),
		field.Int64("highest_revision").Default(0),
		field.String("highest_digest").Default(""),
		field.Bytes("candidate").Optional().Nillable(),
		field.Enum("phase").Values("idle", "accepted", "switching", "applied", "disabling", "disabled", "failed", "interrupted").Default("idle"),
		field.Int64("applied_revision").Default(0),
		field.Bytes("last_good").Optional().Nillable(),
		field.Bool("boot_disabled").Default(false),
		field.Bool("disabled_complete").Default(false),
		field.String("failure_code").Default(""),
		field.Int("owner_pid").Default(0),
		field.Int64("owner_create_time").Default(0),
		field.String("owner_binary").Default(""),
		field.String("owner_config").Default(""),
	}
}

func (RuntimeState) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "runtime_state", Checks: map[string]string{
		"runtime_single_row":        "id = 1",
		"runtime_highest_revision":  "highest_revision >= 0",
		"runtime_applied_revision":  "applied_revision BETWEEN 0 AND highest_revision",
		"runtime_disabled_complete": "disabled_complete = 0 OR (boot_disabled = 1 AND last_good IS NULL)",
		"runtime_owner_pid":         "owner_pid >= 0",
		"runtime_owner_create_time": "owner_create_time >= 0",
		"runtime_owner_group":       "(owner_pid = 0 AND owner_create_time = 0 AND owner_binary = '' AND owner_config = '') OR (owner_pid > 0 AND owner_binary <> '' AND owner_config <> '')",
	}}}
}

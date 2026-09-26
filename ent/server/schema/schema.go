package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type Meta struct{ ent.Schema }

func (Meta) Fields() []ent.Field {
	return []ent.Field{field.String("id").StorageKey("key"), field.String("value")}
}

func (Meta) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "meta"}}
}

type Node struct{ ent.Schema }

func (Node) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").StorageKey("node_id"),
		field.Enum("kind").Values("local", "remote"),
		field.String("name"),
		field.Enum("lifecycle").Values("active", "removing").Default("active"),
		field.String("advertised_frp_host").Optional().Nillable(),
		field.Int("advertised_frp_port").Optional().Nillable(),
		field.String("http_ingress_host").Optional().Nillable(),
		field.Int("http_ingress_port").Optional().Nillable(),
		field.String("created_at"), field.String("updated_at"),
	}
}

func (Node) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("clients", ServerClient.Type).Annotations(entsql.OnDelete(entsql.Restrict)),
		edge.To("pending_clients", ServerClient.Type).Annotations(entsql.OnDelete(entsql.Restrict)),
		edge.To("applied_clients", ServerClient.Type).Annotations(entsql.OnDelete(entsql.SetNull)),
		edge.To("tunnels", Tunnel.Type).Annotations(entsql.OnDelete(entsql.Restrict)),
		edge.To("remote_node", RemoteNode.Type).Unique().Annotations(entsql.OnDelete(entsql.Cascade)),
		edge.To("port_pool", NodePortPool.Type).Unique().Annotations(entsql.OnDelete(entsql.Cascade)),
		edge.To("management_candidate", NodeManagementCandidate.Type).Unique().Annotations(entsql.OnDelete(entsql.Cascade)),
		edge.To("observation", NodeObservation.Type).Unique().Annotations(entsql.OnDelete(entsql.Cascade)),
	}
}

func (Node) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("kind").Unique().StorageKey("nodes_one_local").Annotations(entsql.IndexWhere("kind = 'local'")),
		index.Fields("advertised_frp_host", "advertised_frp_port").Unique().StorageKey("nodes_frp_endpoint").Annotations(entsql.IndexWhere("lifecycle = 'active' AND advertised_frp_host IS NOT NULL")),
	}
}

func (Node) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "nodes", Checks: map[string]string{
		"node_frp_port":     "advertised_frp_port BETWEEN 1 AND 65535",
		"node_http_port":    "http_ingress_port BETWEEN 1 AND 65535",
		"node_frp_pair":     "(advertised_frp_host IS NULL) = (advertised_frp_port IS NULL)",
		"node_http_pair":    "(http_ingress_host IS NULL) = (http_ingress_port IS NULL)",
		"node_local_kind":   "(node_id = 'local') = (kind = 'local')",
		"node_local_active": "node_id <> 'local' OR lifecycle = 'active'",
	}}}
}

type RemoteNode struct{ ent.Schema }

func (RemoteNode) Fields() []ent.Field {
	return []ent.Field{
		field.String("node_id").Unique(),
		field.String("node_public_key").Unique(), field.String("management_address").Unique(),
		field.Int("frp_bind_port"), field.Int("http_vhost_port"),
		field.Int("port_start"), field.Int("port_end"),
		field.Int64("desired_revision").Default(0),
		field.String("desired_hash").Optional().Nillable(),
		field.String("desired_snapshot").Optional().Nillable(),
		field.String("active_token"), field.String("staged_token").Optional().Nillable(),
		field.Int64("staged_token_revision").Optional().Nillable(),
	}
}

func (RemoteNode) Edges() []ent.Edge {
	return []ent.Edge{edge.From("node", Node.Type).Ref("remote_node").Field("node_id").Unique().Required().Annotations(entsql.OnDelete(entsql.Cascade))}
}

func (RemoteNode) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "remote_nodes", Checks: map[string]string{
		"frp_bind_port_range":         "frp_bind_port BETWEEN 1 AND 65535",
		"http_vhost_port_range":       "http_vhost_port BETWEEN 1 AND 65535",
		"remote_port_start_range":     "port_start BETWEEN 1 AND 65535",
		"remote_port_end_range":       "port_end BETWEEN port_start AND 65535",
		"remote_revision_nonnegative": "desired_revision >= 0",
		"staged_revision_nonnegative": "staged_token_revision IS NULL OR staged_token_revision >= 0",
	}}}
}

type NodePortPool struct{ ent.Schema }

func (NodePortPool) Fields() []ent.Field {
	return []ent.Field{
		field.String("node_id").Unique(),
		field.Int("port_start"), field.Int("port_end"),
	}
}

func (NodePortPool) Edges() []ent.Edge {
	return []ent.Edge{edge.From("node", Node.Type).Ref("port_pool").Field("node_id").Unique().Required().Annotations(entsql.OnDelete(entsql.Cascade))}
}

func (NodePortPool) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "node_port_pools", Checks: map[string]string{
		"pool_port_start_range": "port_start BETWEEN 1 AND 65535",
		"pool_port_end_range":   "port_end BETWEEN port_start AND 65535",
	}}}
}

type Account struct{ ent.Schema }

func (Account) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").StorageKey("internal_id"),
		field.Enum("kind").Values("environment", "local"),
		field.String("username"), field.String("username_key").Unique(),
		field.Enum("role").Values("admin", "user"),
		field.String("password_hash").Optional().Nillable(),
		field.String("created_at"), field.String("updated_at"),
	}
}

func (Account) Edges() []ent.Edge {
	return []ent.Edge{edge.To("clients", ServerClient.Type).Annotations(entsql.OnDelete(entsql.Restrict))}
}

func (Account) Indexes() []ent.Index {
	return []ent.Index{index.Fields("kind").Unique().StorageKey("accounts_single_environment").Annotations(entsql.IndexWhere("kind = 'environment'"))}
}

func (Account) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "accounts", Checks: map[string]string{
		"account_kind_role": "(kind = 'environment' AND role = 'admin' AND password_hash IS NULL) OR (kind = 'local' AND password_hash IS NOT NULL)",
	}}}
}

type ServerClient struct{ ent.Schema }

func (ServerClient) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").StorageKey("internal_id"),
		field.String("owner_account_id"), field.String("node_id").Default("local"),
		field.String("pending_node_id").Optional().Nillable(),
		field.String("pending_since").Optional().Nillable(),
		field.String("last_applied_node_id").Optional().Nillable(),
		field.String("remark").Default(""), field.String("token").Unique(),
		field.Int64("desired_revision").Default(0), field.Int64("last_applied_revision").Default(0),
		field.Bool("revocation_pending").Default(false),
		field.Int64("desired_restart_generation").Default(0),
		field.Int64("completed_restart_generation").Default(0),
		field.Int64("restart_error_generation").Optional().Nillable(),
		field.String("restart_error_code").Optional().Nillable(),
		field.String("restart_error_message").Optional().Nillable(),
		field.String("created_at"), field.String("rotated_at").Optional().Nillable(),
	}
}

func (ServerClient) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("owner", Account.Type).Ref("clients").Field("owner_account_id").Unique().Required().Annotations(entsql.OnDelete(entsql.Restrict)),
		edge.From("node", Node.Type).Ref("clients").Field("node_id").Unique().Required().Annotations(entsql.OnDelete(entsql.Restrict)),
		edge.From("pending_node", Node.Type).Ref("pending_clients").Field("pending_node_id").Unique().Annotations(entsql.OnDelete(entsql.Restrict)),
		edge.From("last_applied_node", Node.Type).Ref("applied_clients").Field("last_applied_node_id").Unique(),
		edge.To("tunnels", Tunnel.Type).Annotations(entsql.OnDelete(entsql.Cascade)),
	}
}

func (ServerClient) Indexes() []ent.Index {
	return []ent.Index{index.Fields("owner_account_id", "created_at", "id").StorageKey("clients_by_owner")}
}

func (ServerClient) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "clients", Checks: map[string]string{
		"client_remark_length":        "length(remark) <= 100",
		"client_desired_revision":     "desired_revision >= 0",
		"client_applied_revision":     "last_applied_revision >= 0",
		"client_restart_generation":   "desired_restart_generation >= 0",
		"client_completed_generation": "completed_restart_generation >= 0",
		"client_error_generation":     "restart_error_generation IS NULL OR restart_error_generation >= 0",
	}}}
}

type Tunnel struct{ ent.Schema }

func (Tunnel) Fields() []ent.Field {
	return []ent.Field{
		field.String("id"), field.String("client_internal_id"),
		field.String("node_id").Default("local"), field.String("label").Default(""),
		field.Enum("protocol").Values("http", "tcp", "udp"),
		field.String("custom_domains").Optional().Nillable(),
		field.String("location").Optional().Nillable(),
		field.Int("server_port").Optional().Nillable(),
		field.String("local_host"), field.Int("local_port"),
		field.Bool("enabled").Default(true),
		field.String("options_json").Default("{}"),
		field.String("created_at"), field.String("updated_at"),
	}
}

func (Tunnel) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("client", ServerClient.Type).Ref("tunnels").Field("client_internal_id").Unique().Required().Annotations(entsql.OnDelete(entsql.Cascade)),
		edge.From("node", Node.Type).Ref("tunnels").Field("node_id").Unique().Required().Annotations(entsql.OnDelete(entsql.Restrict)),
		edge.To("routes", TunnelHTTPRoute.Type).Annotations(entsql.OnDelete(entsql.Cascade)),
	}
}

func (Tunnel) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("node_id", "protocol", "server_port").Unique().StorageKey("tunnels_unique_transport_port").Annotations(entsql.IndexWhere("protocol IN ('tcp', 'udp')")),
		index.Fields("client_internal_id", "created_at", "id").StorageKey("tunnels_by_client"),
	}
}

func (Tunnel) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "tunnels", Checks: map[string]string{
		"tunnel_label_length":      "length(label) <= 100",
		"tunnel_server_port_range": "server_port IS NULL OR server_port BETWEEN 1 AND 65535",
		"tunnel_local_port_range":  "local_port BETWEEN 1 AND 65535",
		"tunnel_options_json":      "json_valid(options_json)",
		"tunnel_protocol_shape":    "(protocol = 'http' AND custom_domains IS NOT NULL AND server_port IS NULL) OR (protocol IN ('tcp', 'udp') AND custom_domains IS NULL AND location IS NULL AND server_port IS NOT NULL)",
	}}}
}

type TunnelHTTPRoute struct{ ent.Schema }

func (TunnelHTTPRoute) Fields() []ent.Field {
	return []ent.Field{
		field.String("id"), field.String("tunnel_id"),
		field.String("hostname").Annotations(entsql.Annotation{Collation: "NOCASE"}), field.String("location"),
	}
}

func (TunnelHTTPRoute) Edges() []ent.Edge {
	return []ent.Edge{edge.From("tunnel", Tunnel.Type).Ref("routes").Field("tunnel_id").Unique().Required().Annotations(entsql.OnDelete(entsql.Cascade))}
}

func (TunnelHTTPRoute) Indexes() []ent.Index {
	return []ent.Index{index.Fields("hostname", "location").Unique().StorageKey("tunnel_http_routes_host_location")}
}

func (TunnelHTTPRoute) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "tunnel_http_routes", Checks: map[string]string{
		"route_hostname_lowercase": "hostname COLLATE BINARY = lower(hostname)",
	}}}
}

type NodeManagementCandidate struct{ ent.Schema }

func (NodeManagementCandidate) Fields() []ent.Field {
	return []ent.Field{
		field.String("node_id").Unique(),
		field.String("candidate_address"), field.String("failure_code").Default(""),
	}
}

func (NodeManagementCandidate) Edges() []ent.Edge {
	return []ent.Edge{edge.From("node", Node.Type).Ref("management_candidate").Field("node_id").Unique().Required().Annotations(entsql.OnDelete(entsql.Cascade))}
}

func (NodeManagementCandidate) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "node_management_candidates"}}
}

type NodeObservation struct{ ent.Schema }

func (NodeObservation) Fields() []ent.Field {
	return []ent.Field{
		field.String("node_id").Unique(),
		field.String("status_json").Optional().Nillable(),
		field.String("status_observed_at").Optional().Nillable(),
		field.String("failure_code").Default(""), field.String("attempted_at"),
	}
}

func (NodeObservation) Edges() []ent.Edge {
	return []ent.Edge{edge.From("node", Node.Type).Ref("observation").Field("node_id").Unique().Required().Annotations(entsql.OnDelete(entsql.Cascade))}
}

func (NodeObservation) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "node_observations"}}
}

package server

import "fmt"

type serverHTTPAccountCreateInput struct {
	Username string
	Password string
	Role     AccountRole
}

type serverHTTPAccountRoleInput struct {
	Role AccountRole
}

type serverHTTPAccountPasswordInput struct {
	Password string
}

func (input *serverHTTPAccountPasswordInput) UnmarshalJSON(source []byte) error {
	object, err := serverTunnelJSONObject(source, "password")
	if err != nil {
		return err
	}
	password, err := serverTunnelRequiredString(object, "password")
	if err != nil {
		return err
	}
	input.Password = password
	return nil
}

func (input *serverHTTPAccountRoleInput) UnmarshalJSON(source []byte) error {
	object, err := serverTunnelJSONObject(source, "role")
	if err != nil {
		return err
	}
	role, err := serverTunnelRequiredString(object, "role")
	if err != nil {
		return err
	}
	input.Role = AccountRole(role)
	if input.Role != AccountRoleAdmin && input.Role != AccountRoleUser {
		return fmt.Errorf("role must be admin or user")
	}
	return nil
}

func (input *serverHTTPAccountCreateInput) UnmarshalJSON(source []byte) error {
	object, err := serverTunnelJSONObject(source, "username", "password", "role")
	if err != nil {
		return err
	}
	username, err := serverTunnelRequiredString(object, "username")
	if err != nil {
		return err
	}
	password, err := serverTunnelRequiredString(object, "password")
	if err != nil {
		return err
	}
	role := AccountRoleUser
	if value, err := serverTunnelOptionalString(object, "role", false); err != nil {
		return err
	} else if value != nil {
		role = AccountRole(*value)
	}
	if role != AccountRoleAdmin && role != AccountRoleUser {
		return fmt.Errorf("role must be admin or user")
	}
	input.Username = username
	input.Password = password
	input.Role = role
	return nil
}

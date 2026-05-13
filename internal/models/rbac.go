package models

type RBACUser struct {
	Name  string   `json:"name"`
	Roles []string `json:"roles,omitempty"`
}

type RBACRole struct {
	Name        string           `json:"name"`
	Permissions []RBACPermission `json:"permissions,omitempty"`
}

type RBACPermission struct {
	Type     string `json:"type"` // read | write | readwrite
	Key      string `json:"key"`
	RangeEnd string `json:"rangeEnd,omitempty"`
	Prefix   bool   `json:"prefix,omitempty"`
}

type RBACUserCreate struct {
	Name     string `json:"name"`
	Password string `json:"password"`
}

type RBACRoleGrant struct {
	Role string `json:"role"`
}

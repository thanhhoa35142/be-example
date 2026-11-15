package apikeys

import "strings"

func matchField(grant, want string) bool {
	return grant == "*" || grant == want
}

func evaluate(grants []Grant, resource, action string) bool {
	allowed := false
	for _, g := range grants {
		if !matchField(g.Resource, resource) || !matchField(g.Action, action) {
			continue
		}
		switch strings.ToLower(g.Effect) {
		case "deny":
			return false
		case "allow":
			allowed = true
		}
	}
	return allowed
}

func (a *Auth) Authorize(resource, action string) error {
	if a == nil {
		return ErrInvalidKey
	}
	if !evaluate(a.Grants, resource, action) {
		return ErrForbidden
	}
	return nil
}

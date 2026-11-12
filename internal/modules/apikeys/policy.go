package apikeys

import "strings"

// matchField reports whether a grant field matches a requested value.
// "*" matches anything; otherwise comparison is exact.
func matchField(grant, want string) bool {
	return grant == "*" || grant == want
}

// evaluate applies deny-wins semantics: any matching deny rejects;
// otherwise at least one matching allow is required.
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

// Authorize reports whether the key may perform action on resource.
func (a *Auth) Authorize(resource, action string) error {
	if a == nil {
		return ErrInvalidKey
	}
	if !evaluate(a.Grants, resource, action) {
		return ErrForbidden
	}
	return nil
}

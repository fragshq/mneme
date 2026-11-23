package store

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"strings"
)

// TenantFunc extracts the tenant from a context: one value per tenancy field,
// in the order the store declares its fields, e.g. an organization ID then a
// user ID. Each full combination of values is one isolated space, with its
// own record types, records and sequences. Stores that support tenancy take
// one as an option; nil means tenancy is disabled.
type TenantFunc func(ctx context.Context) []string

// Tenant resolves the tenant for ctx: nil when fn is nil (tenancy disabled),
// fn's values otherwise. It fails with ErrNoTenant when fn returns no value
// or any empty value, so a partial tenant never matches anything. Stores call
// it before touching storage, so a missing tenant never reads or writes
// anything (fail closed).
func Tenant(ctx context.Context, fn TenantFunc) ([]string, error) {
	if fn == nil {
		return nil, nil
	}
	t := fn(ctx)
	if len(t) == 0 || slices.Contains(t, "") {
		return nil, fmt.Errorf("%w (got %q)", ErrNoTenant, t)
	}
	return t, nil
}

// TenantKey encodes tenant values as one string, for stores that key data by
// a single string (key-value stores, maps). Each value is escaped and the
// values are joined with "/", so distinct tenants always get distinct keys:
// ("a/b", "c") and ("a", "b/c") differ. No values encode to "". ParseTenantKey
// reverses it.
func TenantKey(values []string) string {
	parts := make([]string, len(values))
	for i, v := range values {
		parts[i] = url.PathEscape(v)
	}
	return strings.Join(parts, "/")
}

// ParseTenantKey decodes a TenantKey back into its values; "" yields none.
func ParseTenantKey(key string) ([]string, error) {
	if key == "" {
		return []string{}, nil
	}
	parts := strings.Split(key, "/")
	for i, p := range parts {
		v, err := url.PathUnescape(p)
		if err != nil {
			return nil, fmt.Errorf("tenant key %q: %w", key, err)
		}
		parts[i] = v
	}
	return parts, nil
}

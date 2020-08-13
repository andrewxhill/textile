package local

import (
	"context"

	"github.com/textileio/textile/buckets"
)

// GetPathAccessRoles returns access roles for a path.
func (b *Bucket) GetPathAccessRoles(ctx context.Context, pth string) (roles map[string]buckets.Role, err error) {
	ctx, err = b.context(ctx)
	if err != nil {
		return
	}
	return b.clients.Buckets.GetPathAccessRoles(ctx, b.Key(), pth)
}

// EditPathAccessRoles updates path access roles by merging the given roles with existing roles
// and returns the merged roles.
// roles is a map of string marshaled public keys to path roles. A non-nil error is returned
// if the map keys are not unmarshalable to public keys.
// To delete a role for a public key, set its value to buckets.None.
func (b *Bucket) EditPathAccessRoles(ctx context.Context, pth string, roles map[string]buckets.Role) (merged map[string]buckets.Role, err error) {
	ctx, err = b.context(ctx)
	if err != nil {
		return
	}
	err = b.clients.Buckets.EditPathAccessRoles(ctx, b.Key(), pth, roles)
	if err != nil {
		return
	}
	return b.clients.Buckets.GetPathAccessRoles(ctx, b.Key(), pth)
}

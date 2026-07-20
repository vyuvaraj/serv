//go:build !enterprise

package broker

import "context"

// federatedPublish in OSS: no cross-cluster mirroring, returns false.
func federatedPublish(_ context.Context, _ string, _ string) (string, bool) {
	return "", false
}

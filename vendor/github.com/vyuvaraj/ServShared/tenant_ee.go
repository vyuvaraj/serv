//go:build enterprise

package ServShared

import "context"

// isolateTopicImpl in EE: prefixes topic with tenant ID for multi-tenant isolation.
func isolateTopicImpl(ctx context.Context, topic string) string {
	if tid, ok := ctx.Value(TenantContextKey).(string); ok && tid != "" && tid != "default" {
		return tid + "-" + topic
	}
	return topic
}

// isolateDBPoolImpl in EE: prefixes DB name with tenant ID for multi-tenant isolation.
func isolateDBPoolImpl(ctx context.Context, dbName string) string {
	if tid, ok := ctx.Value(TenantContextKey).(string); ok && tid != "" && tid != "default" {
		return tid + "_" + dbName
	}
	return dbName
}

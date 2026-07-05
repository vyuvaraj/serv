//go:build !enterprise

package ServShared

import "context"

// isolateTopicImpl in OSS: no isolation, returns topic unchanged.
func isolateTopicImpl(_ context.Context, topic string) string {
	return topic
}

// isolateDBPoolImpl in OSS: no isolation, returns dbName unchanged.
func isolateDBPoolImpl(_ context.Context, dbName string) string {
	return dbName
}

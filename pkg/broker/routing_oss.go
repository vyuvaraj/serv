//go:build !enterprise

package broker

// filterSemanticRoute is the open-source stub. It always returns false,
// meaning it does not filter messages based on semantic content.
func (e *BrokerEngine) filterSemanticRoute(_ string, _ string) bool {
	return false
}

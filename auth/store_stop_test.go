package auth

import "testing"

func TestStoreStopIsIdempotent(t *testing.T) {
	store := &Store{
		stopCh: make(chan struct{}),
	}

	store.Stop()
	store.Stop()
}

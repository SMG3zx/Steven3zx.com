package natsx

import (
	"context"
	"errors"
	"fmt"

	"github.com/nats-io/nats.go/jetstream"
)

// ArtifactsBucket is the object store of WASM Artifacts, keyed by Build ID.
// The builds service writes it; the copies in builds.db are the record of
// truth, so a missing object is copied back from there.
const ArtifactsBucket = "artifacts"

// DeclareObjectStore creates or updates the object store bucket.
func DeclareObjectStore(ctx context.Context, js jetstream.JetStream, bucket string) (jetstream.ObjectStore, error) {
	store, err := js.CreateOrUpdateObjectStore(ctx, jetstream.ObjectStoreConfig{Bucket: bucket, Storage: jetstream.FileStorage})
	if err != nil {
		return nil, fmt.Errorf("declare the %s object store: %w", bucket, err)
	}
	return store, nil
}

// GetObject returns the bytes of an object. found is false when the object does
// not exist, which is not an error.
func GetObject(ctx context.Context, store jetstream.ObjectStore, name string) (data []byte, found bool, err error) {
	data, err = store.GetBytes(ctx, name)
	if errors.Is(err, jetstream.ErrObjectNotFound) {
		return nil, false, nil
	}
	return data, err == nil, err
}

// DeleteObject deletes an object. Deleting one that does not exist changes
// nothing.
func DeleteObject(ctx context.Context, store jetstream.ObjectStore, name string) error {
	err := store.Delete(ctx, name)
	if errors.Is(err, jetstream.ErrObjectNotFound) {
		return nil
	}
	return err
}

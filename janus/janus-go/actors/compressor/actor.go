package compressor

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/tochemey/goakt/v4/actor"
)

const maxArtifact = 128 << 20
const zstdLevel = 19

// Compress requests compression of a ZIP archive.
type Compress struct{ Archive []byte }

// Compressed reports the Zstandard artifact, a size and duration summary, or an
// error from a compression request.
type Compressed struct {
	Data   []byte
	Report string
	Err    error
}

// Actor compresses build archives for persistence.
type Actor struct{}

// New creates a compressor actor.
func New() *Actor                            { return &Actor{} }
func (*Actor) PreStart(*actor.Context) error { return nil }
func (*Actor) PostStop(*actor.Context) error { return nil }
func (*Actor) Receive(ctx *actor.ReceiveContext) {
	m, ok := ctx.Message().(*Compress)
	if !ok {
		ctx.Unhandled()
		return
	}
	result, err := CompressZstd(ctx.Context(), m.Archive)
	if err != nil {
		ctx.Response(&Compressed{Err: err})
		return
	}
	ctx.Response(result)
}

// CompressZstd compresses a nonempty ZIP archive into one Zstandard frame. The
// input and output are each limited to 128 MiB, and ctx cancels the operation
// before or after compression.
func CompressZstd(ctx context.Context, archive []byte) (*Compressed, error) {
	if len(archive) == 0 || len(archive) > maxArtifact {
		return nil, errors.New("input archive is empty or exceeds 128 MiB")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	started := time.Now()
	encoder, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.EncoderLevelFromZstd(zstdLevel)), zstd.WithEncoderConcurrency(1))
	if err != nil {
		return nil, fmt.Errorf("create Zstandard encoder: %w", err)
	}
	data := encoder.EncodeAll(archive, nil)
	encoder.Close()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(data) == 0 || len(data) > maxArtifact {
		return nil, errors.New("Zstandard artifact is empty or exceeds 128 MiB")
	}
	report := fmt.Sprintf("zstd level %d: %d -> %d bytes in %s", zstdLevel, len(archive), len(data), time.Since(started).Round(time.Microsecond))
	return &Compressed{Data: data, Report: report}, nil
}

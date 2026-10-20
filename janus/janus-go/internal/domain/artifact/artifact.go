// Package artifact owns what an Artifact is: a ZIP of one or more .wasm
// modules, optionally compressed as one Zstandard frame. Builders write it and
// the Wasmtime runner reads it, so the size limit and the path-safety rules
// live here and nowhere else.
package artifact

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"
)

// MaxBytes is the largest Artifact, and the largest module inside one.
const MaxBytes = 128 << 20

// ZstdExtension is appended to an Artifact's name once it is compressed.
const ZstdExtension = ".zst"

const zstdLevel = 19

// SafePath reports whether name may be written beneath a root directory or
// used as an entry name: relative, slash-separated and free of "..", drive
// letters and backslashes.
func SafePath(name string) bool {
	return fs.ValidPath(name) && name != "." && !strings.Contains(name, "\\") && !strings.Contains(name, ":") && !strings.HasPrefix(name, "../")
}

// ZipFromTar turns a tar stream of build output into an Artifact ZIP. Only
// regular .wasm files are kept. It fails when an entry has an unsafe path, the
// output is over MaxBytes, or no module was produced.
func ZipFromTar(data []byte) ([]byte, error) {
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	reader := tar.NewReader(bytes.NewReader(data))
	var total int64
	count := 0
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			_ = writer.Close()
			return nil, fmt.Errorf("read WASM artifact stream: %w", err)
		}
		name := strings.TrimPrefix(path.Clean(header.Name), "./")
		if header.Typeflag != tar.TypeReg || !strings.EqualFold(path.Ext(name), ".wasm") {
			continue
		}
		if !SafePath(name) {
			_ = writer.Close()
			return nil, errors.New("WASM artifact stream contains an unsafe path")
		}
		if header.Size < 0 || header.Size > MaxBytes || total+header.Size > MaxBytes {
			_ = writer.Close()
			return nil, errors.New("WASM output exceeds the 128 MiB artifact limit")
		}
		entry, err := writer.Create(name)
		if err != nil {
			_ = writer.Close()
			return nil, err
		}
		written, err := io.CopyN(entry, reader, header.Size)
		if err != nil || written != header.Size {
			_ = writer.Close()
			return nil, fmt.Errorf("read WASM artifact %s: %w", name, err)
		}
		total += written
		count++
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	if count == 0 {
		return nil, errors.New("build completed without producing a .wasm artifact")
	}
	if buffer.Len() > MaxBytes {
		return nil, errors.New("WASM archive exceeds the 128 MiB artifact limit")
	}
	return buffer.Bytes(), nil
}

// Compressed is an Artifact compressed with Zstandard, with a one-line report
// of the size change.
type Compressed struct {
	Data   []byte
	Report string
}

// Compress compresses a nonempty ZIP Artifact into one Zstandard frame. The
// input and output are each limited to MaxBytes, and ctx cancels the operation
// before or after compression.
func Compress(ctx context.Context, archive []byte) (*Compressed, error) {
	if len(archive) == 0 || len(archive) > MaxBytes {
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
	if len(data) == 0 || len(data) > MaxBytes {
		return nil, errors.New("Zstandard artifact is empty or exceeds 128 MiB")
	}
	report := fmt.Sprintf("zstd level %d: %d -> %d bytes in %s", zstdLevel, len(archive), len(data), time.Since(started).Round(time.Microsecond))
	return &Compressed{Data: data, Report: report}, nil
}

// Module returns the WASM module to run from a stored Artifact. A name ending
// in ".zst" means the data is Zstandard compressed. When the ZIP holds several
// modules the first by name is chosen, and its entry name is returned.
func Module(data []byte, name string) ([]byte, string, error) {
	if strings.HasSuffix(strings.ToLower(name), ZstdExtension) {
		decoder, err := zstd.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, "", err
		}
		defer decoder.Close()
		data, err = io.ReadAll(io.LimitReader(decoder, MaxBytes+1))
		if err != nil {
			return nil, "", err
		}
	}
	if len(data) > MaxBytes {
		return nil, "", errors.New("WASM archive exceeds 128 MiB")
	}
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, "", fmt.Errorf("open WASM archive: %w", err)
	}
	files := make([]*zip.File, 0)
	for _, f := range archive.File {
		if !f.FileInfo().IsDir() && strings.HasSuffix(strings.ToLower(f.Name), ".wasm") {
			files = append(files, f)
		}
	}
	if len(files) == 0 {
		return nil, "", errors.New("artifact contains no WASM module")
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
	file := files[0]
	if file.UncompressedSize64 > MaxBytes {
		return nil, "", errors.New("WASM module exceeds 128 MiB")
	}
	reader, err := file.Open()
	if err != nil {
		return nil, "", err
	}
	defer reader.Close()
	module, err := io.ReadAll(io.LimitReader(reader, MaxBytes+1))
	if err != nil {
		return nil, "", err
	}
	if len(module) > MaxBytes {
		return nil, "", errors.New("WASM module exceeds 128 MiB")
	}
	return module, file.Name, nil
}

package orchestration

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	DefaultHaLowSingleMessageBytes = 60 << 10
	DefaultHaLowChunkBytes         = 8 << 10
	MaxHaLowChunks                 = 4096
)

// HaLowImageManifest describes one correlated image transfer. MQTT and other
// adapters may map these fields onto topics and payloads without changing the
// orchestration contract.
type HaLowImageManifest struct {
	Version          string      `json:"version"`
	RequestID        string      `json:"requestId"`
	CameraID         string      `json:"cameraId"`
	CapturedAt       time.Time   `json:"capturedAt"`
	TimeSynchronized bool        `json:"timeSynchronized"`
	Pose             *CameraPose `json:"pose"`
	MediaType        string      `json:"mediaType"`
	TotalBytes       int         `json:"totalBytes"`
	ChunkCount       int         `json:"chunkCount"`
	SHA256           string      `json:"sha256"`
}

type HaLowImageChunk struct {
	Version   string `json:"version"`
	RequestID string `json:"requestId"`
	CameraID  string `json:"cameraId"`
	Index     int    `json:"index"`
	Data      []byte `json:"data"`
}

// HaLowImageAck is emitted only after the receiver has verified and persisted
// the complete image. Persisted=true tells a store-and-forward camera that its
// cached copy is safe to delete.
type HaLowImageAck struct {
	Version   string `json:"version"`
	RequestID string `json:"requestId"`
	CameraID  string `json:"cameraId"`
	SHA256    string `json:"sha256"`
	Persisted bool   `json:"persisted"`
	Reason    string `json:"reason,omitempty"`
}

func SplitHaLowImage(image Image, chunkBytes int) (HaLowImageManifest, []HaLowImageChunk, error) {
	if chunkBytes <= 0 {
		return HaLowImageManifest{}, nil, errors.New("HaLow chunk bytes must be positive")
	}
	if err := validateImage(image, image.RequestID, len(image.Data)); err != nil {
		return HaLowImageManifest{}, nil, fmt.Errorf("validate HaLow image: %w", err)
	}
	if err := validateCameraPose(image.Pose, image.CapturedAt, 0); err != nil {
		return HaLowImageManifest{}, nil, fmt.Errorf("validate HaLow image pose: %w", err)
	}
	chunkCount := 1 + (len(image.Data)-1)/chunkBytes
	if chunkCount > MaxHaLowChunks {
		return HaLowImageManifest{}, nil, fmt.Errorf(
			"HaLow image requires %d chunks, limit is %d",
			chunkCount,
			MaxHaLowChunks,
		)
	}
	digest := sha256.Sum256(image.Data)
	manifest := HaLowImageManifest{
		Version:          HaLowProtocolVersion,
		RequestID:        image.RequestID,
		CameraID:         image.CameraID,
		CapturedAt:       image.CapturedAt,
		TimeSynchronized: image.TimeSynchronized,
		Pose:             cloneCameraPose(image.Pose),
		MediaType:        image.MediaType,
		TotalBytes:       len(image.Data),
		ChunkCount:       chunkCount,
		SHA256:           hex.EncodeToString(digest[:]),
	}
	chunks := make([]HaLowImageChunk, 0, chunkCount)
	for index, offset := 0, 0; offset < len(image.Data); index, offset = index+1, offset+chunkBytes {
		end := offset + chunkBytes
		if end > len(image.Data) {
			end = len(image.Data)
		}
		chunks = append(chunks, HaLowImageChunk{
			Version:   HaLowProtocolVersion,
			RequestID: image.RequestID,
			CameraID:  image.CameraID,
			Index:     index,
			Data:      append([]byte(nil), image.Data[offset:end]...),
		})
	}
	return manifest, chunks, nil
}

func AssembleHaLowImage(
	manifest HaLowImageManifest,
	chunks []HaLowImageChunk,
	maxImageBytes int,
) (Image, error) {
	if err := validateHaLowManifest(manifest, maxImageBytes); err != nil {
		return Image{}, err
	}
	if len(chunks) != manifest.ChunkCount {
		return Image{}, fmt.Errorf(
			"received %d HaLow chunks, expected %d",
			len(chunks),
			manifest.ChunkCount,
		)
	}

	ordered := make([][]byte, manifest.ChunkCount)
	for _, chunk := range chunks {
		if chunk.Version != HaLowProtocolVersion {
			return Image{}, fmt.Errorf("unsupported HaLow chunk version %q", chunk.Version)
		}
		if chunk.RequestID != manifest.RequestID || chunk.CameraID != manifest.CameraID {
			return Image{}, errors.New("HaLow chunk identity does not match manifest")
		}
		if chunk.Index < 0 || chunk.Index >= manifest.ChunkCount {
			return Image{}, fmt.Errorf("HaLow chunk index %d is out of range", chunk.Index)
		}
		if ordered[chunk.Index] != nil {
			return Image{}, fmt.Errorf("duplicate HaLow chunk index %d", chunk.Index)
		}
		if len(chunk.Data) == 0 {
			return Image{}, fmt.Errorf("HaLow chunk %d is empty", chunk.Index)
		}
		ordered[chunk.Index] = chunk.Data
	}

	data := make([]byte, 0, manifest.TotalBytes)
	for index, chunk := range ordered {
		if chunk == nil {
			return Image{}, fmt.Errorf("HaLow chunk %d is missing", index)
		}
		if len(data) > manifest.TotalBytes-len(chunk) {
			return Image{}, errors.New("HaLow chunks exceed declared image size")
		}
		data = append(data, chunk...)
	}
	if len(data) != manifest.TotalBytes {
		return Image{}, fmt.Errorf(
			"assembled HaLow image has %d bytes, expected %d",
			len(data),
			manifest.TotalBytes,
		)
	}
	expectedDigest, _ := hex.DecodeString(manifest.SHA256)
	actualDigest := sha256.Sum256(data)
	if !bytes.Equal(actualDigest[:], expectedDigest) {
		return Image{}, errors.New("HaLow image SHA-256 mismatch")
	}

	image := Image{
		RequestID:        manifest.RequestID,
		CameraID:         manifest.CameraID,
		CapturedAt:       manifest.CapturedAt,
		TimeSynchronized: manifest.TimeSynchronized,
		Pose:             cloneCameraPose(manifest.Pose),
		MediaType:        manifest.MediaType,
		Data:             data,
	}
	if err := validateImage(image, manifest.RequestID, maxImageBytes); err != nil {
		return Image{}, fmt.Errorf("validate assembled HaLow image: %w", err)
	}
	return image, nil
}

func NewHaLowImageAck(
	manifest HaLowImageManifest,
	persisted bool,
	reason string,
) (HaLowImageAck, error) {
	if err := validateHaLowManifest(manifest, manifest.TotalBytes); err != nil {
		return HaLowImageAck{}, err
	}
	reason = strings.TrimSpace(reason)
	if !persisted && reason == "" {
		return HaLowImageAck{}, errors.New("rejected HaLow image acknowledgement requires a reason")
	}
	return HaLowImageAck{
		Version:   HaLowProtocolVersion,
		RequestID: manifest.RequestID,
		CameraID:  manifest.CameraID,
		SHA256:    manifest.SHA256,
		Persisted: persisted,
		Reason:    reason,
	}, nil
}

// ValidateHaLowImageAck returns nil only when the receiver confirms that the
// exact manifest was durably persisted. A camera may delete its cached copy
// only after this succeeds.
func ValidateHaLowImageAck(manifest HaLowImageManifest, ack HaLowImageAck) error {
	if err := validateHaLowManifest(manifest, manifest.TotalBytes); err != nil {
		return err
	}
	if ack.Version != HaLowProtocolVersion {
		return fmt.Errorf("unsupported HaLow acknowledgement version %q", ack.Version)
	}
	if ack.RequestID != manifest.RequestID ||
		ack.CameraID != manifest.CameraID ||
		ack.SHA256 != manifest.SHA256 {
		return errors.New("HaLow acknowledgement does not match manifest")
	}
	if !ack.Persisted {
		reason := strings.TrimSpace(ack.Reason)
		if reason == "" {
			reason = "receiver rejected image"
		}
		return fmt.Errorf("HaLow image was not persisted: %s", reason)
	}
	return nil
}

func validateHaLowManifest(manifest HaLowImageManifest, maxImageBytes int) error {
	if manifest.Version != HaLowProtocolVersion {
		return fmt.Errorf("unsupported HaLow manifest version %q", manifest.Version)
	}
	if strings.TrimSpace(manifest.RequestID) == "" {
		return errors.New("HaLow manifest request ID is required")
	}
	if strings.TrimSpace(manifest.CameraID) == "" {
		return errors.New("HaLow manifest camera ID is required")
	}
	if manifest.CapturedAt.IsZero() {
		return errors.New("HaLow manifest capture time is required")
	}
	if err := validateCameraPose(manifest.Pose, manifest.CapturedAt, 0); err != nil {
		return fmt.Errorf("validate HaLow manifest pose: %w", err)
	}
	if maxImageBytes <= 0 {
		return errors.New("maximum image bytes must be positive")
	}
	if manifest.TotalBytes <= 0 || manifest.TotalBytes > maxImageBytes {
		return fmt.Errorf(
			"HaLow manifest image size %d is outside limit %d",
			manifest.TotalBytes,
			maxImageBytes,
		)
	}
	if manifest.ChunkCount <= 0 || manifest.ChunkCount > MaxHaLowChunks {
		return fmt.Errorf(
			"HaLow manifest chunk count %d is outside limit %d",
			manifest.ChunkCount,
			MaxHaLowChunks,
		)
	}
	digest, err := hex.DecodeString(manifest.SHA256)
	if err != nil || len(digest) != sha256.Size {
		return errors.New("HaLow manifest SHA-256 is invalid")
	}
	return nil
}

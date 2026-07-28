// Package orchestration coordinates the physical inputs needed by an edge-AI
// workload. It deliberately contains no MQTT, Meshtastic, camera-driver, or AI
// SDK dependency; those implementations plug into the small interfaces below.
package orchestration

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	HaLowProtocolVersion = "halow.capture/v1"

	DefaultCaptureTimeout = 15 * time.Second
	DefaultMaxCaptureSkew = 2 * time.Second
	DefaultMaxImageBytes  = 8 << 20
	DefaultMaxPoseAge     = 5 * time.Second
)

var ErrCaptureSkew = errors.New("paired captures exceed maximum skew")
var ErrUnsynchronizedCaptureTime = errors.New("paired captures require synchronized timestamps")

// Config bounds one paired capture operation.
type Config struct {
	CaptureTimeout                time.Duration
	MaxCaptureSkew                time.Duration
	MaxImageBytes                 int
	RequireSynchronizedTimestamps bool
	RequireCameraPose             bool
	MaxPoseAge                    time.Duration
}

func DefaultConfig() Config {
	return Config{
		CaptureTimeout:                DefaultCaptureTimeout,
		MaxCaptureSkew:                DefaultMaxCaptureSkew,
		MaxImageBytes:                 DefaultMaxImageBytes,
		RequireSynchronizedTimestamps: true,
		RequireCameraPose:             true,
		MaxPoseAge:                    DefaultMaxPoseAge,
	}
}

func (c Config) Validate() error {
	var errs []error
	if c.CaptureTimeout <= 0 {
		errs = append(errs, errors.New("capture timeout must be positive"))
	}
	if c.MaxCaptureSkew <= 0 {
		errs = append(errs, errors.New("maximum capture skew must be positive"))
	}
	if c.MaxImageBytes <= 0 {
		errs = append(errs, errors.New("maximum image bytes must be positive"))
	}
	if c.MaxPoseAge < 0 || (c.RequireCameraPose && c.MaxPoseAge == 0) {
		errs = append(
			errs,
			errors.New("maximum camera pose age must be positive when camera pose is required"),
		)
	}
	return errors.Join(errs...)
}

// CaptureRequest is shared by both cameras so their replies can be correlated.
type CaptureRequest struct {
	ID          string    `json:"id"`
	RequestedAt time.Time `json:"requestedAt"`
}

// Image is the normalized frame passed through the orchestrator.
type Image struct {
	RequestID        string      `json:"requestId"`
	CameraID         string      `json:"cameraId"`
	CapturedAt       time.Time   `json:"capturedAt"`
	TimeSynchronized bool        `json:"timeSynchronized"`
	Pose             *CameraPose `json:"pose,omitempty"`
	MediaType        string      `json:"mediaType"`
	Data             []byte      `json:"data"`
}

// ImagePair preserves the role of each frame for multi-view inference. The
// additional source can be HaLow or any future transport-backed camera.
type ImagePair struct {
	RequestID   string    `json:"requestId"`
	RequestedAt time.Time `json:"requestedAt"`
	Primary     Image     `json:"primary"`
	Additional  Image     `json:"additional"`
}

// ImageSource can be a directly connected camera or any remote camera.
type ImageSource interface {
	Capture(context.Context, CaptureRequest) (Image, error)
}

type ImageSourceFunc func(context.Context, CaptureRequest) (Image, error)

func (f ImageSourceFunc) Capture(ctx context.Context, request CaptureRequest) (Image, error) {
	return f(ctx, request)
}

// PairAnalyzer is the minimal AI boundary. It is called only after both images
// have passed correlation, pose, media, size, timestamp, and skew validation.
type PairAnalyzer interface {
	AnalyzePair(context.Context, ImagePair) error
}

type PairAnalyzerFunc func(context.Context, ImagePair) error

func (f PairAnalyzerFunc) AnalyzePair(ctx context.Context, pair ImagePair) error {
	return f(ctx, pair)
}

func validateRequest(request CaptureRequest) error {
	if strings.TrimSpace(request.ID) == "" {
		return errors.New("capture request ID is required")
	}
	if request.RequestedAt.IsZero() {
		return errors.New("capture request time is required")
	}
	return nil
}

func validateImage(image Image, requestID string, maxBytes int) error {
	if image.RequestID != requestID {
		return fmt.Errorf(
			"image request ID %q does not match capture request %q",
			image.RequestID,
			requestID,
		)
	}
	if strings.TrimSpace(image.CameraID) == "" {
		return errors.New("camera ID is required")
	}
	if image.CapturedAt.IsZero() {
		return errors.New("capture time is required")
	}
	mediaType := strings.ToLower(strings.TrimSpace(image.MediaType))
	if !strings.HasPrefix(mediaType, "image/") {
		return fmt.Errorf("media type %q is not an image", image.MediaType)
	}
	if len(image.Data) == 0 {
		return errors.New("image payload is empty")
	}
	if len(image.Data) > maxBytes {
		return fmt.Errorf("image payload has %d bytes, limit is %d", len(image.Data), maxBytes)
	}
	if strings.SplitN(mediaType, ";", 2)[0] == "image/jpeg" &&
		(len(image.Data) < 4 ||
			image.Data[0] != 0xff ||
			image.Data[1] != 0xd8 ||
			image.Data[len(image.Data)-2] != 0xff ||
			image.Data[len(image.Data)-1] != 0xd9) {
		return errors.New("JPEG payload is incomplete")
	}
	return nil
}

func cloneImage(image Image) Image {
	result := image
	result.Pose = cloneCameraPose(image.Pose)
	result.Data = append([]byte(nil), image.Data...)
	return result
}

func clonePair(pair ImagePair) ImagePair {
	result := pair
	result.Primary = cloneImage(pair.Primary)
	result.Additional = cloneImage(pair.Additional)
	return result
}

func absoluteDuration(value time.Duration) time.Duration {
	if value < 0 {
		return -value
	}
	return value
}

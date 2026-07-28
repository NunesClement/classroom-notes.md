package orchestration

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// CameraFrame is the device-facing result before request correlation metadata
// is attached.
type CameraFrame struct {
	CapturedAt       time.Time
	TimeSynchronized bool
	Pose             *CameraPose
	MediaType        string
	Data             []byte
}

type Camera interface {
	Capture(context.Context) (CameraFrame, error)
}

type CameraFunc func(context.Context) (CameraFrame, error)

func (f CameraFunc) Capture(ctx context.Context) (CameraFrame, error) {
	return f(ctx)
}

// CameraSource adapts a directly connected camera to an orchestrated source.
type CameraSource struct {
	cameraID      string
	camera        Camera
	maxImageBytes int
}

func NewCameraSource(cameraID string, camera Camera, maxImageBytes int) (*CameraSource, error) {
	cameraID = strings.TrimSpace(cameraID)
	if cameraID == "" {
		return nil, errors.New("camera ID is required")
	}
	if isNil(camera) {
		return nil, errors.New("camera is required")
	}
	if maxImageBytes <= 0 {
		return nil, errors.New("maximum image bytes must be positive")
	}
	return &CameraSource{
		cameraID:      cameraID,
		camera:        camera,
		maxImageBytes: maxImageBytes,
	}, nil
}

func (s *CameraSource) Capture(ctx context.Context, request CaptureRequest) (Image, error) {
	if s == nil {
		return Image{}, errors.New("camera source is nil")
	}
	if err := validateRequest(request); err != nil {
		return Image{}, err
	}
	frame, err := s.camera.Capture(ctx)
	if err != nil {
		return Image{}, err
	}
	image := Image{
		RequestID:        request.ID,
		CameraID:         s.cameraID,
		CapturedAt:       frame.CapturedAt.UTC(),
		TimeSynchronized: frame.TimeSynchronized,
		Pose:             cloneCameraPose(frame.Pose),
		MediaType:        frame.MediaType,
		Data:             append([]byte(nil), frame.Data...),
	}
	if err := validateImage(image, request.ID, s.maxImageBytes); err != nil {
		return Image{}, err
	}
	return image, nil
}

// HaLowCaptureRequest and HaLowCaptureResponse form the versioned request/reply
// contract implemented by an MQTT-over-Wi-Fi-HaLow or other transport adapter.
type HaLowCaptureRequest struct {
	Version        string    `json:"version"`
	RequestID      string    `json:"requestId"`
	RequestedAt    time.Time `json:"requestedAt"`
	TargetCameraID string    `json:"targetCameraId"`
}

type HaLowCaptureResponse struct {
	Version   string `json:"version"`
	RequestID string `json:"requestId"`
	Image     Image  `json:"image"`
}

type HaLowProtocol interface {
	Capture(context.Context, HaLowCaptureRequest) (HaLowCaptureResponse, error)
}

type HaLowProtocolFunc func(context.Context, HaLowCaptureRequest) (HaLowCaptureResponse, error)

func (f HaLowProtocolFunc) Capture(
	ctx context.Context,
	request HaLowCaptureRequest,
) (HaLowCaptureResponse, error) {
	return f(ctx, request)
}

// RemoteHaLowSource is the THOR-side adapter for a camera reached through the
// Wi-Fi HaLow application protocol.
type RemoteHaLowSource struct {
	cameraID string
	protocol HaLowProtocol
}

func NewRemoteHaLowSource(cameraID string, protocol HaLowProtocol) (*RemoteHaLowSource, error) {
	cameraID = strings.TrimSpace(cameraID)
	if cameraID == "" {
		return nil, errors.New("HaLow camera ID is required")
	}
	if isNil(protocol) {
		return nil, errors.New("HaLow protocol is required")
	}
	return &RemoteHaLowSource{cameraID: cameraID, protocol: protocol}, nil
}

func (s *RemoteHaLowSource) Capture(
	ctx context.Context,
	request CaptureRequest,
) (Image, error) {
	if s == nil {
		return Image{}, errors.New("remote HaLow source is nil")
	}
	if err := validateRequest(request); err != nil {
		return Image{}, err
	}
	response, err := s.protocol.Capture(ctx, HaLowCaptureRequest{
		Version:        HaLowProtocolVersion,
		RequestID:      request.ID,
		RequestedAt:    request.RequestedAt,
		TargetCameraID: s.cameraID,
	})
	if err != nil {
		return Image{}, err
	}
	if response.Version != HaLowProtocolVersion {
		return Image{}, fmt.Errorf("unsupported HaLow protocol version %q", response.Version)
	}
	if response.RequestID != request.ID {
		return Image{}, fmt.Errorf(
			"HaLow response request ID %q does not match %q",
			response.RequestID,
			request.ID,
		)
	}
	if response.Image.RequestID != request.ID {
		return Image{}, fmt.Errorf(
			"HaLow image request ID %q does not match %q",
			response.Image.RequestID,
			request.ID,
		)
	}
	if response.Image.CameraID != s.cameraID {
		return Image{}, fmt.Errorf(
			"HaLow response camera ID %q does not match target %q",
			response.Image.CameraID,
			s.cameraID,
		)
	}
	return cloneImage(response.Image), nil
}

// HaLowEndpoint is the camera-side protocol handler. A concrete transport
// decodes a request, calls HandleCapture, and encodes the returned response.
type HaLowEndpoint struct {
	source *CameraSource
}

func NewHaLowEndpoint(cameraID string, camera Camera, maxImageBytes int) (*HaLowEndpoint, error) {
	source, err := NewCameraSource(cameraID, camera, maxImageBytes)
	if err != nil {
		return nil, err
	}
	return &HaLowEndpoint{source: source}, nil
}

func (e *HaLowEndpoint) HandleCapture(
	ctx context.Context,
	request HaLowCaptureRequest,
) (HaLowCaptureResponse, error) {
	if e == nil || e.source == nil {
		return HaLowCaptureResponse{}, errors.New("HaLow endpoint is nil")
	}
	if request.Version != HaLowProtocolVersion {
		return HaLowCaptureResponse{}, fmt.Errorf(
			"unsupported HaLow protocol version %q",
			request.Version,
		)
	}
	if request.TargetCameraID != e.source.cameraID {
		return HaLowCaptureResponse{}, fmt.Errorf(
			"capture target %q does not match camera %q",
			request.TargetCameraID,
			e.source.cameraID,
		)
	}
	captureRequest := CaptureRequest{
		ID:          request.RequestID,
		RequestedAt: request.RequestedAt,
	}
	image, err := e.source.Capture(ctx, captureRequest)
	if err != nil {
		return HaLowCaptureResponse{}, err
	}
	return HaLowCaptureResponse{
		Version:   HaLowProtocolVersion,
		RequestID: request.RequestID,
		Image:     image,
	}, nil
}

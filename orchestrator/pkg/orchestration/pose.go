package orchestration

import (
	"errors"
	"fmt"
	"math"
	"time"
)

type PositionSource string

const (
	PositionSourceGPS      PositionSource = "gps"
	PositionSourceSurveyed PositionSource = "surveyed"
	PositionSourceNetwork  PositionSource = "network"
)

type OrientationReference string

const OrientationTrueNorth OrientationReference = "true_north"

var (
	ErrMissingCameraPose = errors.New("camera position and view are required")
	ErrStaleCameraPose   = errors.New("camera view is stale")
)

// GeoPosition locates the camera rather than the observed object. A fixed
// camera may report a surveyed position; a mobile camera should report its
// latest GPS or network-derived fix.
type GeoPosition struct {
	LatitudeDegrees          float64        `json:"latitudeDegrees"`
	LongitudeDegrees         float64        `json:"longitudeDegrees"`
	AltitudeMeters           *float64       `json:"altitudeMeters,omitempty"`
	HorizontalAccuracyMeters float64        `json:"horizontalAccuracyMeters"`
	FixAt                    time.Time      `json:"fixAt"`
	Source                   PositionSource `json:"source"`
}

// PTZState describes where the camera was looking for one captured frame.
// Pan is clockwise from true north, tilt is -90 degrees down to +90 degrees
// up, and roll is clockwise around the optical axis.
type PTZState struct {
	Reference            OrientationReference `json:"reference"`
	PanDegrees           float64              `json:"panDegrees"`
	TiltDegrees          float64              `json:"tiltDegrees"`
	RollDegrees          float64              `json:"rollDegrees"`
	ZoomRatio            float64              `json:"zoomRatio"`
	HorizontalFOVDegrees float64              `json:"horizontalFovDegrees"`
	VerticalFOVDegrees   float64              `json:"verticalFovDegrees"`
	ObservedAt           time.Time            `json:"observedAt"`
}

// CameraPose is captured with every image because a PTZ camera can move
// between frames. Fixed cameras report their calibrated, constant view.
type CameraPose struct {
	Position GeoPosition `json:"position"`
	View     PTZState    `json:"view"`
}

func validateCameraPose(pose *CameraPose, capturedAt time.Time, maxViewAge time.Duration) error {
	if pose == nil {
		return ErrMissingCameraPose
	}
	if err := validateGeoPosition(pose.Position); err != nil {
		return fmt.Errorf("validate camera position: %w", err)
	}
	if err := validatePTZState(pose.View); err != nil {
		return fmt.Errorf("validate camera view: %w", err)
	}
	if pose.Position.FixAt.After(capturedAt) {
		return errors.New("camera position fix is after image capture")
	}
	if pose.View.ObservedAt.After(capturedAt) {
		return errors.New("camera view observation is after image capture")
	}
	if maxViewAge > 0 {
		age := capturedAt.Sub(pose.View.ObservedAt)
		if age > maxViewAge {
			return fmt.Errorf("%w: got %s, limit is %s", ErrStaleCameraPose, age, maxViewAge)
		}
	}
	return nil
}

func validateGeoPosition(position GeoPosition) error {
	if !finite(position.LatitudeDegrees) ||
		position.LatitudeDegrees < -90 ||
		position.LatitudeDegrees > 90 {
		return errors.New("latitude must be finite and between -90 and 90 degrees")
	}
	if !finite(position.LongitudeDegrees) ||
		position.LongitudeDegrees < -180 ||
		position.LongitudeDegrees > 180 {
		return errors.New("longitude must be finite and between -180 and 180 degrees")
	}
	if position.AltitudeMeters != nil && !finite(*position.AltitudeMeters) {
		return errors.New("altitude must be finite when provided")
	}
	if !finite(position.HorizontalAccuracyMeters) || position.HorizontalAccuracyMeters <= 0 {
		return errors.New("horizontal accuracy must be finite and positive")
	}
	if position.FixAt.IsZero() {
		return errors.New("position fix time is required")
	}
	switch position.Source {
	case PositionSourceGPS, PositionSourceSurveyed, PositionSourceNetwork:
	default:
		return fmt.Errorf("unsupported position source %q", position.Source)
	}
	return nil
}

func validatePTZState(view PTZState) error {
	if view.Reference != OrientationTrueNorth {
		return fmt.Errorf("unsupported orientation reference %q", view.Reference)
	}
	if !finite(view.PanDegrees) || view.PanDegrees < 0 || view.PanDegrees >= 360 {
		return errors.New("pan must be finite and in [0, 360) degrees")
	}
	if !finite(view.TiltDegrees) || view.TiltDegrees < -90 || view.TiltDegrees > 90 {
		return errors.New("tilt must be finite and between -90 and 90 degrees")
	}
	if !finite(view.RollDegrees) || view.RollDegrees < -180 || view.RollDegrees > 180 {
		return errors.New("roll must be finite and between -180 and 180 degrees")
	}
	if !finite(view.ZoomRatio) || view.ZoomRatio < 1 {
		return errors.New("zoom ratio must be finite and at least 1")
	}
	if !finite(view.HorizontalFOVDegrees) ||
		view.HorizontalFOVDegrees <= 0 ||
		view.HorizontalFOVDegrees > 360 {
		return errors.New("horizontal field of view must be finite and in (0, 360] degrees")
	}
	if !finite(view.VerticalFOVDegrees) ||
		view.VerticalFOVDegrees <= 0 ||
		view.VerticalFOVDegrees > 180 {
		return errors.New("vertical field of view must be finite and in (0, 180] degrees")
	}
	if view.ObservedAt.IsZero() {
		return errors.New("view observation time is required")
	}
	return nil
}

func cloneCameraPose(pose *CameraPose) *CameraPose {
	if pose == nil {
		return nil
	}
	result := *pose
	if pose.Position.AltitudeMeters != nil {
		altitude := *pose.Position.AltitudeMeters
		result.Position.AltitudeMeters = &altitude
	}
	return &result
}

func finite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

package orchestration

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync/atomic"
	"time"
)

type RequestIDGenerator func(time.Time) string

type CoordinatorOption func(*Coordinator)

func WithClock(clock func() time.Time) CoordinatorOption {
	return func(coordinator *Coordinator) {
		coordinator.clock = clock
	}
}

func WithRequestIDGenerator(generator RequestIDGenerator) CoordinatorOption {
	return func(coordinator *Coordinator) {
		coordinator.requestID = generator
	}
}

// Coordinator owns one primary/additional camera pair. It serializes sessions
// so a second inference cannot interleave captures on the same physical
// devices.
type Coordinator struct {
	config     Config
	primary    ImageSource
	additional ImageSource
	analyzer   PairAnalyzer
	clock      func() time.Time
	requestID  RequestIDGenerator
	session    chan struct{}
}

var requestSequence atomic.Uint64

func NewCoordinator(
	config Config,
	primary ImageSource,
	additional ImageSource,
	analyzer PairAnalyzer,
	options ...CoordinatorOption,
) (*Coordinator, error) {
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("validate orchestration config: %w", err)
	}
	if isNil(primary) {
		return nil, errors.New("primary image source is required")
	}
	if isNil(additional) {
		return nil, errors.New("additional image source is required")
	}
	if isNil(analyzer) {
		return nil, errors.New("pair analyzer is required")
	}
	result := &Coordinator{
		config:     config,
		primary:    primary,
		additional: additional,
		analyzer:   analyzer,
		clock:      time.Now,
		requestID: func(now time.Time) string {
			return fmt.Sprintf("capture-%d-%d", now.UnixNano(), requestSequence.Add(1))
		},
		session: make(chan struct{}, 1),
	}
	result.session <- struct{}{}
	for _, option := range options {
		option(result)
	}
	if result.clock == nil {
		return nil, errors.New("clock cannot be nil")
	}
	if result.requestID == nil {
		return nil, errors.New("request ID generator cannot be nil")
	}
	return result, nil
}

type captureResult struct {
	role  string
	image Image
	err   error
}

// CaptureAndAnalyze captures both viewpoints concurrently and delivers the
// complete pair to the AI. A partial, oversized, skewed, or uncorrelated pair
// is returned as an error and is never sent to the analyzer.
func (c *Coordinator) CaptureAndAnalyze(ctx context.Context) (ImagePair, error) {
	if ctx == nil {
		return ImagePair{}, errors.New("context cannot be nil")
	}
	select {
	case <-ctx.Done():
		return ImagePair{}, ctx.Err()
	case <-c.session:
	}
	defer func() {
		c.session <- struct{}{}
	}()

	now := c.clock().UTC()
	if now.IsZero() {
		return ImagePair{}, errors.New("clock returned a zero time")
	}
	request := CaptureRequest{
		ID:          strings.TrimSpace(c.requestID(now)),
		RequestedAt: now,
	}
	if err := validateRequest(request); err != nil {
		return ImagePair{}, err
	}

	captureContext, cancel := context.WithTimeout(ctx, c.config.CaptureTimeout)
	results := make(chan captureResult, 2)
	capture := func(role string, source ImageSource) {
		image, err := source.Capture(captureContext, request)
		results <- captureResult{role: role, image: image, err: err}
	}
	go capture("primary", c.primary)
	go capture("additional", c.additional)

	var primary Image
	var additional Image
	for completed := 0; completed < 2; completed++ {
		select {
		case <-captureContext.Done():
			cancel()
			return ImagePair{}, fmt.Errorf("capture image pair: %w", captureContext.Err())
		case result := <-results:
			if result.err != nil {
				cancel()
				return ImagePair{}, fmt.Errorf("capture %s image: %w", result.role, result.err)
			}
			if err := validateImage(result.image, request.ID, c.config.MaxImageBytes); err != nil {
				cancel()
				return ImagePair{}, fmt.Errorf("validate %s image: %w", result.role, err)
			}
			if result.image.Pose == nil && !c.config.RequireCameraPose {
				// Pose reporting may be disabled for legacy sources. Any pose
				// that is present is still validated below.
			} else if err := validateCameraPose(
				result.image.Pose,
				result.image.CapturedAt,
				c.config.MaxPoseAge,
			); err != nil {
				cancel()
				return ImagePair{}, fmt.Errorf("validate %s image pose: %w", result.role, err)
			}
			if result.role == "primary" {
				primary = cloneImage(result.image)
			} else {
				additional = cloneImage(result.image)
			}
		}
	}
	cancel()

	timestampsSynchronized := primary.TimeSynchronized && additional.TimeSynchronized
	if c.config.RequireSynchronizedTimestamps && !timestampsSynchronized {
		return ImagePair{}, ErrUnsynchronizedCaptureTime
	}
	if timestampsSynchronized {
		skew := absoluteDuration(primary.CapturedAt.Sub(additional.CapturedAt))
		if skew > c.config.MaxCaptureSkew {
			return ImagePair{}, fmt.Errorf(
				"%w: got %s, limit is %s",
				ErrCaptureSkew,
				skew,
				c.config.MaxCaptureSkew,
			)
		}
	}
	pair := ImagePair{
		RequestID:   request.ID,
		RequestedAt: request.RequestedAt,
		Primary:     primary,
		Additional:  additional,
	}
	if err := c.analyzer.AnalyzePair(ctx, clonePair(pair)); err != nil {
		return clonePair(pair), fmt.Errorf("analyze image pair: %w", err)
	}
	return clonePair(pair), nil
}

// isNil catches both a nil interface and a typed-nil implementation.
func isNil(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	}
	return false
}

package orchestration

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var testNow = time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)

func TestCoordinatorCapturesHaLowPairAndDeliversItToAnalyzer(t *testing.T) {
	config := DefaultConfig()
	primary, err := NewCameraSource(
		"thor-camera",
		CameraFunc(func(context.Context) (CameraFrame, error) {
			return CameraFrame{
				CapturedAt:       testNow.Add(100 * time.Millisecond),
				TimeSynchronized: true,
				Pose:             testPose(testNow.Add(100 * time.Millisecond)),
				MediaType:        "image/jpeg",
				Data:             jpegPayload("primary"),
			}, nil
		}),
		config.MaxImageBytes,
	)
	if err != nil {
		t.Fatal(err)
	}
	endpoint, err := NewHaLowEndpoint(
		"halow-camera",
		CameraFunc(func(context.Context) (CameraFrame, error) {
			return CameraFrame{
				CapturedAt:       testNow.Add(300 * time.Millisecond),
				TimeSynchronized: true,
				Pose:             testPose(testNow.Add(300 * time.Millisecond)),
				MediaType:        "image/jpeg",
				Data:             jpegPayload("halow"),
			}, nil
		}),
		config.MaxImageBytes,
	)
	if err != nil {
		t.Fatal(err)
	}

	var wireRequest HaLowCaptureRequest
	protocol := HaLowProtocolFunc(func(
		ctx context.Context,
		request HaLowCaptureRequest,
	) (HaLowCaptureResponse, error) {
		wireRequest = request
		return endpoint.HandleCapture(ctx, request)
	})
	halow, err := NewRemoteHaLowSource("halow-camera", protocol)
	if err != nil {
		t.Fatal(err)
	}

	var analyzed ImagePair
	analyzer := PairAnalyzerFunc(func(_ context.Context, pair ImagePair) error {
		analyzed = clonePair(pair)
		pair.Primary.Data[0] = 'X'
		*pair.Primary.Pose.Position.AltitudeMeters = -1
		return nil
	})
	coordinator, err := NewCoordinator(
		config,
		primary,
		halow,
		analyzer,
		WithClock(func() time.Time { return testNow }),
		WithRequestIDGenerator(func(time.Time) string { return "capture-001" }),
	)
	if err != nil {
		t.Fatal(err)
	}

	pair, err := coordinator.CaptureAndAnalyze(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if pair.RequestID != "capture-001" || pair.RequestID != analyzed.RequestID {
		t.Fatalf("unexpected request correlation: pair=%q analyzed=%q", pair.RequestID, analyzed.RequestID)
	}
	if pair.Primary.CameraID != "thor-camera" || pair.Additional.CameraID != "halow-camera" {
		t.Fatalf("unexpected camera roles: %+v", pair)
	}
	if !bytes.Equal(pair.Primary.Data, jpegPayload("primary")) ||
		!bytes.Equal(pair.Additional.Data, jpegPayload("halow")) {
		t.Fatalf(
			"unexpected image payloads: primary=%q additional=%q",
			pair.Primary.Data,
			pair.Additional.Data,
		)
	}
	if altitude := *pair.Primary.Pose.Position.AltitudeMeters; altitude != 181 {
		t.Fatalf("analyzer mutated camera pose; altitude=%v", altitude)
	}
	wantWireRequest := HaLowCaptureRequest{
		Version:        HaLowProtocolVersion,
		RequestID:      "capture-001",
		RequestedAt:    testNow,
		TargetCameraID: "halow-camera",
	}
	if !reflect.DeepEqual(wireRequest, wantWireRequest) {
		t.Fatalf("wire request:\n got %+v\nwant %+v", wireRequest, wantWireRequest)
	}
}

func TestCoordinatorRejectsSkewedPairBeforeAnalysis(t *testing.T) {
	var analysisCalls atomic.Int32
	coordinator := testCoordinator(
		t,
		func(request CaptureRequest) Image {
			return testImage(request.ID, "primary", testNow, "primary")
		},
		func(request CaptureRequest) Image {
			return testImage(request.ID, "halow", testNow.Add(3*time.Second), "halow")
		},
		PairAnalyzerFunc(func(context.Context, ImagePair) error {
			analysisCalls.Add(1)
			return nil
		}),
	)

	_, err := coordinator.CaptureAndAnalyze(context.Background())
	if !errors.Is(err, ErrCaptureSkew) {
		t.Fatalf("got error %v, want ErrCaptureSkew", err)
	}
	if calls := analysisCalls.Load(); calls != 0 {
		t.Fatalf("analyzer was called %d times for an invalid pair", calls)
	}
}

func TestCoordinatorRejectsUncorrelatedHaLowResponse(t *testing.T) {
	var analysisCalls atomic.Int32
	coordinator := testCoordinator(
		t,
		func(request CaptureRequest) Image {
			return testImage(request.ID, "primary", testNow, "primary")
		},
		func(request CaptureRequest) Image {
			return testImage("old-request", "halow", testNow, "halow")
		},
		PairAnalyzerFunc(func(context.Context, ImagePair) error {
			analysisCalls.Add(1)
			return nil
		}),
	)

	_, err := coordinator.CaptureAndAnalyze(context.Background())
	if err == nil || !strings.Contains(err.Error(), "does not match capture request") {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls := analysisCalls.Load(); calls != 0 {
		t.Fatalf("analyzer was called %d times for an uncorrelated pair", calls)
	}
}

func TestCoordinatorRejectsUnsynchronizedCaptureTime(t *testing.T) {
	var analysisCalls atomic.Int32
	coordinator := testCoordinator(
		t,
		func(request CaptureRequest) Image {
			return testImage(request.ID, "primary", testNow, "primary")
		},
		func(request CaptureRequest) Image {
			image := testImage(request.ID, "halow", testNow, "halow")
			image.TimeSynchronized = false
			return image
		},
		PairAnalyzerFunc(func(context.Context, ImagePair) error {
			analysisCalls.Add(1)
			return nil
		}),
	)

	_, err := coordinator.CaptureAndAnalyze(context.Background())
	if !errors.Is(err, ErrUnsynchronizedCaptureTime) {
		t.Fatalf("got error %v, want ErrUnsynchronizedCaptureTime", err)
	}
	if calls := analysisCalls.Load(); calls != 0 {
		t.Fatalf("analyzer was called %d times for unsynchronized images", calls)
	}
}

func TestCoordinatorCanAllowUnsynchronizedBestEffortPair(t *testing.T) {
	config := DefaultConfig()
	config.RequireSynchronizedTimestamps = false
	primary := ImageSourceFunc(func(
		_ context.Context,
		request CaptureRequest,
	) (Image, error) {
		return testImage(request.ID, "primary", testNow, "primary"), nil
	})
	additional := ImageSourceFunc(func(
		_ context.Context,
		request CaptureRequest,
	) (Image, error) {
		image := testImage(request.ID, "halow", testNow.Add(time.Hour), "halow")
		image.TimeSynchronized = false
		return image, nil
	})
	var analysisCalls atomic.Int32
	coordinator, err := NewCoordinator(
		config,
		primary,
		additional,
		PairAnalyzerFunc(func(context.Context, ImagePair) error {
			analysisCalls.Add(1)
			return nil
		}),
		WithClock(func() time.Time { return testNow }),
		WithRequestIDGenerator(func(time.Time) string { return "capture-001" }),
	)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := coordinator.CaptureAndAnalyze(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls := analysisCalls.Load(); calls != 1 {
		t.Fatalf("analyzer calls=%d, want 1", calls)
	}
}

func TestCoordinatorRejectsMissingCameraPoseBeforeAnalysis(t *testing.T) {
	var analysisCalls atomic.Int32
	coordinator := testCoordinator(
		t,
		func(request CaptureRequest) Image {
			image := testImage(request.ID, "primary", testNow, "primary")
			image.Pose = nil
			return image
		},
		func(request CaptureRequest) Image {
			return testImage(request.ID, "halow", testNow, "halow")
		},
		PairAnalyzerFunc(func(context.Context, ImagePair) error {
			analysisCalls.Add(1)
			return nil
		}),
	)

	_, err := coordinator.CaptureAndAnalyze(context.Background())
	if !errors.Is(err, ErrMissingCameraPose) {
		t.Fatalf("got error %v, want ErrMissingCameraPose", err)
	}
	if calls := analysisCalls.Load(); calls != 0 {
		t.Fatalf("analyzer was called %d times for an image without a pose", calls)
	}
}

func TestCoordinatorRejectsStaleCameraViewBeforeAnalysis(t *testing.T) {
	var analysisCalls atomic.Int32
	coordinator := testCoordinator(
		t,
		func(request CaptureRequest) Image {
			image := testImage(request.ID, "primary", testNow, "primary")
			image.Pose.View.ObservedAt = testNow.Add(-DefaultMaxPoseAge - time.Millisecond)
			return image
		},
		func(request CaptureRequest) Image {
			return testImage(request.ID, "halow", testNow, "halow")
		},
		PairAnalyzerFunc(func(context.Context, ImagePair) error {
			analysisCalls.Add(1)
			return nil
		}),
	)

	_, err := coordinator.CaptureAndAnalyze(context.Background())
	if !errors.Is(err, ErrStaleCameraPose) {
		t.Fatalf("got error %v, want ErrStaleCameraPose", err)
	}
	if calls := analysisCalls.Load(); calls != 0 {
		t.Fatalf("analyzer was called %d times for a stale pose", calls)
	}
}

func TestCoordinatorCanAllowLegacySourceWithoutCameraPose(t *testing.T) {
	config := DefaultConfig()
	config.RequireCameraPose = false
	config.MaxPoseAge = 0
	primary := ImageSourceFunc(func(
		_ context.Context,
		request CaptureRequest,
	) (Image, error) {
		image := testImage(request.ID, "primary", testNow, "primary")
		image.Pose = nil
		return image, nil
	})
	additional := ImageSourceFunc(func(
		_ context.Context,
		request CaptureRequest,
	) (Image, error) {
		return testImage(request.ID, "halow", testNow, "halow"), nil
	})
	coordinator, err := NewCoordinator(
		config,
		primary,
		additional,
		PairAnalyzerFunc(func(context.Context, ImagePair) error { return nil }),
		WithClock(func() time.Time { return testNow }),
		WithRequestIDGenerator(func(time.Time) string { return "capture-001" }),
	)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := coordinator.CaptureAndAnalyze(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestCoordinatorSerializesPhysicalCaptureSessions(t *testing.T) {
	config := DefaultConfig()
	firstEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	var primaryCalls atomic.Int32
	primary := ImageSourceFunc(func(
		ctx context.Context,
		request CaptureRequest,
	) (Image, error) {
		if primaryCalls.Add(1) == 1 {
			close(firstEntered)
			select {
			case <-ctx.Done():
				return Image{}, ctx.Err()
			case <-releaseFirst:
			}
		}
		return testImage(request.ID, "primary", testNow, "primary"), nil
	})
	halow := ImageSourceFunc(func(
		_ context.Context,
		request CaptureRequest,
	) (Image, error) {
		return testImage(request.ID, "halow", testNow, "halow"), nil
	})
	coordinator, err := NewCoordinator(
		config,
		primary,
		halow,
		PairAnalyzerFunc(func(context.Context, ImagePair) error { return nil }),
		WithClock(func() time.Time { return testNow }),
		WithRequestIDGenerator(sequenceGenerator()),
	)
	if err != nil {
		t.Fatal(err)
	}

	var wait sync.WaitGroup
	wait.Add(2)
	errs := make(chan error, 2)
	go func() {
		defer wait.Done()
		_, captureErr := coordinator.CaptureAndAnalyze(context.Background())
		errs <- captureErr
	}()
	<-firstEntered
	go func() {
		defer wait.Done()
		_, captureErr := coordinator.CaptureAndAnalyze(context.Background())
		errs <- captureErr
	}()

	time.Sleep(20 * time.Millisecond)
	if calls := primaryCalls.Load(); calls != 1 {
		t.Fatalf("second session entered the camera early; calls=%d", calls)
	}
	close(releaseFirst)
	wait.Wait()
	close(errs)
	for captureErr := range errs {
		if captureErr != nil {
			t.Fatal(captureErr)
		}
	}
	if calls := primaryCalls.Load(); calls != 2 {
		t.Fatalf("primary capture calls=%d, want 2", calls)
	}
}

func TestHaLowEndpointRejectsWrongTargetWithoutTouchingCamera(t *testing.T) {
	var cameraCalls atomic.Int32
	endpoint, err := NewHaLowEndpoint(
		"halow-camera",
		CameraFunc(func(context.Context) (CameraFrame, error) {
			cameraCalls.Add(1)
			return CameraFrame{}, nil
		}),
		DefaultMaxImageBytes,
	)
	if err != nil {
		t.Fatal(err)
	}

	_, err = endpoint.HandleCapture(context.Background(), HaLowCaptureRequest{
		Version:        HaLowProtocolVersion,
		RequestID:      "capture-001",
		RequestedAt:    testNow,
		TargetCameraID: "another-camera",
	})
	if err == nil {
		t.Fatal("wrong HaLow target was accepted")
	}
	if calls := cameraCalls.Load(); calls != 0 {
		t.Fatalf("camera was touched %d times", calls)
	}
}

func testCoordinator(
	t *testing.T,
	primaryImage func(CaptureRequest) Image,
	additionalImage func(CaptureRequest) Image,
	analyzer PairAnalyzer,
) *Coordinator {
	t.Helper()
	config := DefaultConfig()
	primary := ImageSourceFunc(func(
		_ context.Context,
		request CaptureRequest,
	) (Image, error) {
		return primaryImage(request), nil
	})
	additional := ImageSourceFunc(func(
		_ context.Context,
		request CaptureRequest,
	) (Image, error) {
		return additionalImage(request), nil
	})
	coordinator, err := NewCoordinator(
		config,
		primary,
		additional,
		analyzer,
		WithClock(func() time.Time { return testNow }),
		WithRequestIDGenerator(func(time.Time) string { return "capture-001" }),
	)
	if err != nil {
		t.Fatal(err)
	}
	return coordinator
}

func testImage(requestID, cameraID string, capturedAt time.Time, data string) Image {
	return Image{
		RequestID:        requestID,
		CameraID:         cameraID,
		CapturedAt:       capturedAt,
		TimeSynchronized: true,
		Pose:             testPose(capturedAt),
		MediaType:        "image/jpeg",
		Data:             jpegPayload(data),
	}
}

func testPose(observedAt time.Time) *CameraPose {
	altitudeMeters := 181.0
	return &CameraPose{
		Position: GeoPosition{
			LatitudeDegrees:          41.8781,
			LongitudeDegrees:         -87.6298,
			AltitudeMeters:           &altitudeMeters,
			HorizontalAccuracyMeters: 3,
			FixAt:                    observedAt.Add(-time.Minute),
			Source:                   PositionSourceGPS,
		},
		View: PTZState{
			Reference:            OrientationTrueNorth,
			PanDegrees:           45,
			TiltDegrees:          -10,
			RollDegrees:          0,
			ZoomRatio:            1,
			HorizontalFOVDegrees: 70,
			VerticalFOVDegrees:   45,
			ObservedAt:           observedAt,
		},
	}
}

func jpegPayload(contents string) []byte {
	result := []byte{0xff, 0xd8}
	result = append(result, []byte(contents)...)
	return append(result, 0xff, 0xd9)
}

func sequenceGenerator() RequestIDGenerator {
	var sequence atomic.Int32
	return func(time.Time) string {
		return "capture-" + time.Unix(int64(sequence.Add(1)), 0).UTC().Format("150405")
	}
}

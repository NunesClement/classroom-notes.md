package orchestration

import (
	"math"
	"strings"
	"testing"
)

func TestValidateCameraPoseAcceptsSurveyedFixedCamera(t *testing.T) {
	pose := testPose(testNow)
	pose.Position.Source = PositionSourceSurveyed
	pose.Position.FixAt = testNow.AddDate(-1, 0, 0)

	if err := validateCameraPose(pose, testNow, DefaultMaxPoseAge); err != nil {
		t.Fatal(err)
	}
}

func TestValidateCameraPoseRejectsInvalidCoordinates(t *testing.T) {
	pose := testPose(testNow)
	pose.Position.LatitudeDegrees = math.NaN()

	err := validateCameraPose(pose, testNow, DefaultMaxPoseAge)
	if err == nil || !strings.Contains(err.Error(), "latitude") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateCameraPoseRejectsAmbiguousPanReference(t *testing.T) {
	pose := testPose(testNow)
	pose.View.Reference = ""

	err := validateCameraPose(pose, testNow, DefaultMaxPoseAge)
	if err == nil || !strings.Contains(err.Error(), "orientation reference") {
		t.Fatalf("unexpected error: %v", err)
	}
}

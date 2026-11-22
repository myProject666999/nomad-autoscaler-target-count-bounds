// Copyright IBM Corp. 2026
// SPDX-License-Identifier: MPL-2.0

package plugin

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/autoscaling/types"
	"github.com/hashicorp/nomad-autoscaler/sdk"
	"github.com/shoenig/test/must"
)

func TestTargetPlugin_applyTargetBounds(t *testing.T) {

	testCases := []struct {
		name string

		asg    *types.AutoScalingGroup
		action sdk.ScalingAction

		expectErr        bool
		expectCount      int64
		expectDirection  sdk.ScaleDirection
		expectCapped     bool
		expectNoOp       bool
		expectMismatch   bool
		expectFinalCount int64 // value of action.Count after the call
		expectMetaCapped bool  // whether the capped meta key is present
	}{
		{
			name: "request above max size is clamped onto scale out",
			asg: &types.AutoScalingGroup{
				AutoScalingGroupName: aws.String("test-asg"),
				MinSize:              aws.Int32(2),
				MaxSize:              aws.Int32(8),
				DesiredCapacity:      aws.Int32(6),
			},
			action:           sdk.ScalingAction{Count: 12, Direction: sdk.ScaleDirectionUp},
			expectCount:      8,
			expectDirection:  sdk.ScaleDirectionUp,
			expectCapped:     true,
			expectFinalCount: 8,
			expectMetaCapped: true,
		},
		{
			name: "clamp landing on the current count is a no-op",
			asg: &types.AutoScalingGroup{
				AutoScalingGroupName: aws.String("test-asg"),
				MinSize:              aws.Int32(2),
				MaxSize:              aws.Int32(8),
				DesiredCapacity:      aws.Int32(8),
			},
			action:          sdk.ScalingAction{Count: 12, Direction: sdk.ScaleDirectionUp},
			expectCount:     8,
			expectDirection: sdk.ScaleDirectionNone,
			expectCapped:    true,
			expectNoOp:      true,
			// The explicit Up direction contradicts the effective None.
			expectMismatch:   true,
			expectFinalCount: 8,
			expectMetaCapped: true,
		},
		{
			name: "equal request on an out-of-bounds current count is not pulled back",
			asg: &types.AutoScalingGroup{
				AutoScalingGroupName: aws.String("test-asg"),
				MinSize:              aws.Int32(2),
				MaxSize:              aws.Int32(4),
				DesiredCapacity:      aws.Int32(6),
			},
			action:           sdk.ScalingAction{Count: 6, Direction: sdk.ScaleDirectionNone},
			expectCount:      6,
			expectDirection:  sdk.ScaleDirectionNone,
			expectCapped:     false,
			expectNoOp:       true,
			expectFinalCount: 6,
			expectMetaCapped: false,
		},
		{
			name: "request below an over-max current clamps to max and scales down",
			asg: &types.AutoScalingGroup{
				AutoScalingGroupName: aws.String("test-asg"),
				MinSize:              aws.Int32(2),
				MaxSize:              aws.Int32(4),
				DesiredCapacity:      aws.Int32(6),
			},
			action:           sdk.ScalingAction{Count: 5, Direction: sdk.ScaleDirectionDown},
			expectCount:      4,
			expectDirection:  sdk.ScaleDirectionDown,
			expectCapped:     true,
			expectFinalCount: 4,
			expectMetaCapped: true,
		},
		{
			name: "request below min size is clamped up",
			asg: &types.AutoScalingGroup{
				AutoScalingGroupName: aws.String("test-asg"),
				MinSize:              aws.Int32(2),
				MaxSize:              aws.Int32(8),
				DesiredCapacity:      aws.Int32(6),
			},
			action:           sdk.ScalingAction{Count: 1, Direction: sdk.ScaleDirectionDown},
			expectCount:      2,
			expectDirection:  sdk.ScaleDirectionDown,
			expectCapped:     true,
			expectFinalCount: 2,
			expectMetaCapped: true,
		},
		{
			name: "direction conflict follows the count and warns",
			asg: &types.AutoScalingGroup{
				AutoScalingGroupName: aws.String("test-asg"),
				DesiredCapacity:      aws.Int32(6),
			},
			action:           sdk.ScalingAction{Count: 3, Direction: sdk.ScaleDirectionUp},
			expectCount:      3,
			expectDirection:  sdk.ScaleDirectionDown,
			expectMismatch:   true,
			expectFinalCount: 3,
			expectMetaCapped: false,
		},
		{
			name: "nil bounds pass through unchanged",
			asg: &types.AutoScalingGroup{
				AutoScalingGroupName: aws.String("test-asg"),
				DesiredCapacity:      aws.Int32(6),
			},
			action:           sdk.ScalingAction{Count: 9, Direction: sdk.ScaleDirectionUp},
			expectCount:      9,
			expectDirection:  sdk.ScaleDirectionUp,
			expectFinalCount: 9,
			expectMetaCapped: false,
		},
		{
			name: "nil desired capacity returns an error",
			asg: &types.AutoScalingGroup{
				AutoScalingGroupName: aws.String("test-asg"),
				MinSize:              aws.Int32(2),
				MaxSize:              aws.Int32(8),
			},
			action:    sdk.ScalingAction{Count: 5, Direction: sdk.ScaleDirectionUp},
			expectErr: true,
		},
		{
			name: "min greater than max returns an error",
			asg: &types.AutoScalingGroup{
				AutoScalingGroupName: aws.String("test-asg"),
				MinSize:              aws.Int32(10),
				MaxSize:              aws.Int32(2),
				DesiredCapacity:      aws.Int32(5),
			},
			action:    sdk.ScalingAction{Count: 7, Direction: sdk.ScaleDirectionUp},
			expectErr: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tp := newTestPlugin()
			action := tc.action
			// Deliberately leave Meta nil to prove Canonicalize initializes it.
			action.Meta = nil

			res, err := tp.applyTargetBounds(tc.asg, &action)

			if tc.expectErr {
				must.Error(t, err)
				return
			}

			must.NoError(t, err)
			must.Eq(t, int64(*tc.asg.DesiredCapacity), res.Current)
			must.Eq(t, tc.expectCount, res.Count)
			must.Eq(t, tc.expectDirection, res.Direction)
			must.Eq(t, tc.expectCapped, res.Capped)
			must.Eq(t, tc.expectNoOp, res.NoOp)
			must.Eq(t, tc.expectMismatch, res.DirectionMismatch)
			must.Eq(t, tc.expectFinalCount, action.Count)

			// The original request is always preserved and the action count
			// was rewritten. Meta is JSON-encoded across gRPC in production, so
			// assert the in-process types stored here.
			original, ok := action.Meta[sdk.TargetMetaKeyCountOriginal]
			must.True(t, ok)
			originalCount, ok := original.(int64)
			must.True(t, ok)
			must.Eq(t, tc.action.Count, originalCount)

			capped, ok := action.Meta[sdk.TargetMetaKeyCountCapped]
			must.Eq(t, tc.expectMetaCapped, ok)
			if ok {
				cappedBool, ok := capped.(bool)
				must.True(t, ok)
				must.True(t, cappedBool)
			}

			if res.Reason != "" {
				metaReason, ok := action.Meta[sdk.TargetMetaKeyCountReason].(string)
				must.True(t, ok)
				must.Eq(t, res.Reason, metaReason)
			}
		})
	}
}

func TestTargetPlugin_applyTargetBounds_scaleOutNoOpContract(t *testing.T) {
	// When the ASG is already at MaxSize, the adapter must report a no-op so
	// Scale returns before ever calling the (nil in tests) autoscaling client.
	tp := newTestPlugin()
	asg := &types.AutoScalingGroup{
		AutoScalingGroupName: aws.String("test-asg"),
		MaxSize:              aws.Int32(8),
		DesiredCapacity:      aws.Int32(8),
	}
	action := sdk.ScalingAction{Count: 12, Direction: sdk.ScaleDirectionUp}

	res, err := tp.applyTargetBounds(asg, &action)
	must.NoError(t, err)
	must.True(t, res.NoOp)
	must.True(t, res.Capped)
	must.Eq(t, int64(8), res.Count)
}

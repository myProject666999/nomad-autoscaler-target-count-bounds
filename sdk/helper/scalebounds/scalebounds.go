// Copyright IBM Corp. 2026
// SPDX-License-Identifier: MPL-2.0

// Package scalebounds contains a cloud-agnostic, side-effect-free function
// that applies a target's own capacity bounds (for example an AWS Auto
// Scaling Group's MinSize/MaxSize) to a scaling request expressed as an
// absolute desired count.
//
// It is intentionally separate from the policy-layer
// sdk.ScalingAction.CapCount: policy Min/Max and the cloud target's physical
// bounds are two independent layers, each recording its own meta keys.
package scalebounds

import (
	"fmt"
	"strings"

	"github.com/hashicorp/nomad-autoscaler/sdk"
)

// Options carries a single scaling decision together with the target's
// current state and optional capacity bounds.
type Options struct {

	// Current is the target's current capacity (for example an ASG's
	// DesiredCapacity).
	Current int64

	// Desired is the requested absolute target count, after any policy-layer
	// capping has been applied. It must never be the dry-run sentinel
	// (sdk.StrategyActionMetaValueDryRunCount); callers return before
	// invoking Apply in dry-run mode.
	Desired int64

	// Min is the optional lower bound. A nil pointer means the lower side is
	// unbounded.
	Min *int64

	// Max is the optional upper bound. A nil pointer means the upper side is
	// unbounded.
	Max *int64

	// Direction is the direction claimed by the ScalingAction. It is advisory
	// only and is never trusted to decide the effective direction: the
	// effective direction is always derived from comparing the resulting
	// count against Current.
	Direction sdk.ScaleDirection
}

// Result is the outcome of applying the bounds to a scaling request.
type Result struct {

	// Current echoes Options.Current so callers can derive scale-in deltas
	// without keeping a separate copy of the input.
	Current int64

	// Count is the desired count after clamping. It equals Desired when no
	// bound was hit, otherwise it equals the violated bound.
	Count int64

	// Direction is the effective direction, derived solely from comparing
	// Count to Current. It is independent of Options.Direction.
	Direction sdk.ScaleDirection

	// Capped is true when Desired was changed to satisfy a bound. It is
	// always false when Desired equals Current, because such requests are
	// left untouched (the target is never proactively pulled back onto a
	// bound).
	Capped bool

	// NoOp is true when Count equals Current. Two distinct cases collapse
	// here:
	//
	//   - Desired equals Current and nothing was clamped ("don't pull back");
	//   - clamping moved a different Desired onto Current.
	//
	// Callers translate this into an sdk.TargetScalingNoOpError so the
	// policy is not placed into cooldown.
	NoOp bool

	// DirectionMismatch is true when Options.Direction is an explicit
	// ScaleDirectionUp or ScaleDirectionDown that contradicts the effective
	// Direction. ScaleDirectionNone and ScaleDirectionRecommendation never
	// set this flag. Callers should emit a warning log when true.
	DirectionMismatch bool

	// Reason is a machine-stable, human-readable explanation. It is empty
	// when the request is unchanged and the direction agrees. It is intended
	// to be stored under the sdk.TargetMetaKeyCountReason meta key and/or
	// appended to the action reason.
	Reason string
}

// Apply clamps opts.Desired into [Min, Max] (nil bounds mean unrestricted)
// and derives the effective direction from the clamped count versus the
// current count.
//
// When Desired already equals Current, Apply short-circuits: it never clamps
// and never proactively moves the target back onto a bound, even when the
// current count itself sits outside the bounds. The result is a NoOp in that
// case.
//
// Apply performs no arithmetic on the int64 inputs besides comparison, so it
// is safe at math.MinInt64/math.MaxInt64. It returns an error only for the
// invalid configuration where both bounds are set and Min is greater than
// Max.
func Apply(opts Options) (Result, error) {

	if opts.Min != nil && opts.Max != nil && *opts.Min > *opts.Max {
		return Result{}, fmt.Errorf(
			"scalebounds: min bound %d is greater than max bound %d",
			*opts.Min, *opts.Max)
	}

	result := Result{Current: opts.Current, Count: opts.Desired}
	reasons := []string{}

	// A request that matches the current count is a no-op. Do not clamp it:
	// the target must not be changed beyond what the strategy requested, so
	// an out-of-bounds current count is never proactively pulled back.
	if opts.Desired != opts.Current {
		if opts.Min != nil && result.Count < *opts.Min {
			reasons = append(reasons, fmt.Sprintf(
				"desired count %d is below target min %d", opts.Desired, *opts.Min))
			result.Count = *opts.Min
			result.Capped = true
		}
		if opts.Max != nil && result.Count > *opts.Max {
			reasons = append(reasons, fmt.Sprintf(
				"desired count %d is above target max %d", opts.Desired, *opts.Max))
			result.Count = *opts.Max
			result.Capped = true
		}
	}

	switch {
	case result.Count < opts.Current:
		result.Direction = sdk.ScaleDirectionDown
	case result.Count > opts.Current:
		result.Direction = sdk.ScaleDirectionUp
	default:
		result.Direction = sdk.ScaleDirectionNone
		result.NoOp = true
	}

	// Only explicit up/down directions can contradict the effective
	// direction. None carries no claim, and Recommendation never reaches a
	// target plugin.
	switch opts.Direction {
	case sdk.ScaleDirectionUp:
		if result.Direction != sdk.ScaleDirectionUp {
			result.DirectionMismatch = true
			reasons = append(reasons, fmt.Sprintf(
				"requested direction up disagrees with effective direction %s",
				result.Direction))
		}
	case sdk.ScaleDirectionDown:
		if result.Direction != sdk.ScaleDirectionDown {
			result.DirectionMismatch = true
			reasons = append(reasons, fmt.Sprintf(
				"requested direction down disagrees with effective direction %s",
				result.Direction))
		}
	}

	result.Reason = strings.Join(reasons, "; ")
	return result, nil
}

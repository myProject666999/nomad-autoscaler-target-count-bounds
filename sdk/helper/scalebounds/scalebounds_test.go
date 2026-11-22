// Copyright IBM Corp. 2026
// SPDX-License-Identifier: MPL-2.0

package scalebounds

import (
	"math"
	"testing"

	"github.com/hashicorp/nomad-autoscaler/sdk"
	"github.com/shoenig/test/must"
)

func ptr64(v int64) *int64 { return &v }

func TestApply(t *testing.T) {

	testCases := []struct {
		name string
		opts Options

		expectErr         bool
		expectCount       int64
		expectDirection   sdk.ScaleDirection
		expectCapped      bool
		expectNoOp        bool
		expectMismatch    bool
		expectReasonEmpty bool
	}{
		// A. Size relationships without touching a bound.
		{
			name:              "A1 scale up within bounds",
			opts:              Options{Current: 6, Desired: 8, Min: ptr64(2), Max: ptr64(10), Direction: sdk.ScaleDirectionUp},
			expectCount:       8,
			expectDirection:   sdk.ScaleDirectionUp,
			expectReasonEmpty: true,
		},
		{
			name:              "A2 scale down within bounds",
			opts:              Options{Current: 6, Desired: 4, Min: ptr64(2), Max: ptr64(10), Direction: sdk.ScaleDirectionDown},
			expectCount:       4,
			expectDirection:   sdk.ScaleDirectionDown,
			expectReasonEmpty: true,
		},
		{
			name:              "A3 equal counts is noop",
			opts:              Options{Current: 6, Desired: 6, Min: ptr64(2), Max: ptr64(10), Direction: sdk.ScaleDirectionNone},
			expectCount:       6,
			expectDirection:   sdk.ScaleDirectionNone,
			expectNoOp:        true,
			expectReasonEmpty: true,
		},

		// B. Single-side clamping.
		{
			name:            "B1 desired below min is clamped up",
			opts:            Options{Current: 6, Desired: 1, Min: ptr64(2), Max: ptr64(10), Direction: sdk.ScaleDirectionDown},
			expectCount:     2,
			expectDirection: sdk.ScaleDirectionDown,
			expectCapped:    true,
		},
		{
			name:            "B2 desired above max is clamped down (acceptance 1)",
			opts:            Options{Current: 6, Desired: 12, Min: ptr64(2), Max: ptr64(8), Direction: sdk.ScaleDirectionUp},
			expectCount:     8,
			expectDirection: sdk.ScaleDirectionUp,
			expectCapped:    true,
		},
		{
			name:            "B3 clamp lands on current count so noop",
			opts:            Options{Current: 8, Desired: 12, Min: ptr64(2), Max: ptr64(8), Direction: sdk.ScaleDirectionUp},
			expectCount:     8,
			expectDirection: sdk.ScaleDirectionNone,
			expectCapped:    true,
			expectNoOp:      true,
			// The explicit Up direction contradicts the effective None.
			expectMismatch:    true,
			expectReasonEmpty: false,
		},
		{
			name:            "B4 clamp flips direction and flags mismatch",
			opts:            Options{Current: 6, Desired: 3, Min: ptr64(4), Max: ptr64(10), Direction: sdk.ScaleDirectionUp},
			expectCount:     4,
			expectDirection: sdk.ScaleDirectionDown,
			expectCapped:    true,
			expectMismatch:  true,
		},

		// C. Current count out of bounds and equal-count requests.
		{
			name:              "C1 equal request while current above max is not pulled back (acceptance: max4 cur6 request6)",
			opts:              Options{Current: 6, Desired: 6, Min: ptr64(2), Max: ptr64(4), Direction: sdk.ScaleDirectionUp},
			expectCount:       6,
			expectDirection:   sdk.ScaleDirectionNone,
			expectNoOp:        true,
			expectCapped:      false,
			expectMismatch:    true, // explicit Up contradicts effective None
			expectReasonEmpty: false,
		},
		{
			name:            "C2 equal request while current below min is not pulled back",
			opts:            Options{Current: 1, Desired: 1, Min: ptr64(2), Max: ptr64(8), Direction: sdk.ScaleDirectionDown},
			expectCount:     1,
			expectDirection: sdk.ScaleDirectionNone,
			expectNoOp:      true,
			expectCapped:    false,
			expectMismatch:  true,
		},
		{
			name:            "C3 request below over-max current clamps to max and goes down (max4 cur6 request5)",
			opts:            Options{Current: 6, Desired: 5, Min: ptr64(2), Max: ptr64(4), Direction: sdk.ScaleDirectionDown},
			expectCount:     4,
			expectDirection: sdk.ScaleDirectionDown,
			expectCapped:    true,
		},
		{
			name:            "C4 min equal to max is a valid fixed bound",
			opts:            Options{Current: 5, Desired: 9, Min: ptr64(3), Max: ptr64(3), Direction: sdk.ScaleDirectionUp},
			expectCount:     3,
			expectDirection: sdk.ScaleDirectionDown,
			expectCapped:    true,
			expectMismatch:  true,
		},

		// D. Missing bounds.
		{
			name:              "D1 nil min means no lower bound",
			opts:              Options{Current: 6, Desired: 1, Min: nil, Max: ptr64(10), Direction: sdk.ScaleDirectionDown},
			expectCount:       1,
			expectDirection:   sdk.ScaleDirectionDown,
			expectReasonEmpty: true,
		},
		{
			name:              "D2 nil max means no upper bound",
			opts:              Options{Current: 6, Desired: 99, Min: ptr64(2), Max: nil, Direction: sdk.ScaleDirectionUp},
			expectCount:       99,
			expectDirection:   sdk.ScaleDirectionUp,
			expectReasonEmpty: true,
		},
		{
			name:              "D3 both bounds nil passes through",
			opts:              Options{Current: 6, Desired: 99, Min: nil, Max: nil, Direction: sdk.ScaleDirectionUp},
			expectCount:       99,
			expectDirection:   sdk.ScaleDirectionUp,
			expectReasonEmpty: true,
		},
		{
			name:              "D4 both bounds nil and equal counts is noop",
			opts:              Options{Current: 6, Desired: 6, Min: nil, Max: nil, Direction: sdk.ScaleDirectionNone},
			expectCount:       6,
			expectDirection:   sdk.ScaleDirectionNone,
			expectNoOp:        true,
			expectReasonEmpty: true,
		},

		// E. Invalid configuration.
		{
			name:      "E1 min greater than max is a configuration error",
			opts:      Options{Current: 5, Desired: 7, Min: ptr64(10), Max: ptr64(2), Direction: sdk.ScaleDirectionUp},
			expectErr: true,
		},

		// F. int64 extremes; Apply never adds or subtracts.
		{
			name:            "F1 max int64 desired clamped to upper bound",
			opts:            Options{Current: 0, Desired: math.MaxInt64, Min: nil, Max: ptr64(8), Direction: sdk.ScaleDirectionUp},
			expectCount:     8,
			expectDirection: sdk.ScaleDirectionUp,
			expectCapped:    true,
		},
		{
			name:            "F2 min int64 desired clamped to lower bound",
			opts:            Options{Current: 0, Desired: math.MinInt64, Min: ptr64(-8), Max: nil, Direction: sdk.ScaleDirectionDown},
			expectCount:     -8,
			expectDirection: sdk.ScaleDirectionDown,
			expectCapped:    true,
		},
		{
			name:              "F3 max int64 current with smaller desired",
			opts:              Options{Current: math.MaxInt64, Desired: math.MaxInt64 - 1, Min: nil, Max: nil, Direction: sdk.ScaleDirectionDown},
			expectCount:       math.MaxInt64 - 1,
			expectDirection:   sdk.ScaleDirectionDown,
			expectReasonEmpty: true,
		},
		{
			name:              "F4 min int64 current with larger desired",
			opts:              Options{Current: math.MinInt64, Desired: math.MinInt64 + 1, Min: nil, Max: nil, Direction: sdk.ScaleDirectionUp},
			expectCount:       math.MinInt64 + 1,
			expectDirection:   sdk.ScaleDirectionUp,
			expectReasonEmpty: true,
		},
		{
			name:              "F5 extreme equal counts are noop without overflow",
			opts:              Options{Current: math.MaxInt64, Desired: math.MaxInt64, Min: nil, Max: nil, Direction: sdk.ScaleDirectionNone},
			expectCount:       math.MaxInt64,
			expectDirection:   sdk.ScaleDirectionNone,
			expectNoOp:        true,
			expectReasonEmpty: true,
		},

		// G. Direction conflicts.
		{
			name:            "G1 up direction with lower count goes down (acceptance 3)",
			opts:            Options{Current: 6, Desired: 3, Min: nil, Max: nil, Direction: sdk.ScaleDirectionUp},
			expectCount:     3,
			expectDirection: sdk.ScaleDirectionDown,
			expectMismatch:  true,
		},
		{
			name:            "G2 down direction with higher count goes up",
			opts:            Options{Current: 3, Desired: 6, Min: nil, Max: nil, Direction: sdk.ScaleDirectionDown},
			expectCount:     6,
			expectDirection: sdk.ScaleDirectionUp,
			expectMismatch:  true,
		},
		{
			name:            "G3 up direction on equal counts is mismatch plus noop",
			opts:            Options{Current: 6, Desired: 6, Min: nil, Max: nil, Direction: sdk.ScaleDirectionUp},
			expectCount:     6,
			expectDirection: sdk.ScaleDirectionNone,
			expectNoOp:      true,
			expectMismatch:  true,
		},
		{
			name:              "G4 none direction with differing count follows the count",
			opts:              Options{Current: 6, Desired: 8, Min: nil, Max: nil, Direction: sdk.ScaleDirectionNone},
			expectCount:       8,
			expectDirection:   sdk.ScaleDirectionUp,
			expectMismatch:    false,
			expectReasonEmpty: true,
		},
		{
			name:              "G5 recommendation direction with lower count is not a mismatch",
			opts:              Options{Current: 6, Desired: 4, Min: nil, Max: nil, Direction: sdk.ScaleDirectionRecommendation},
			expectCount:       4,
			expectDirection:   sdk.ScaleDirectionDown,
			expectMismatch:    false,
			expectReasonEmpty: true,
		},
		{
			name:              "G6 recommendation direction on equal counts is noop without mismatch",
			opts:              Options{Current: 6, Desired: 6, Min: nil, Max: nil, Direction: sdk.ScaleDirectionRecommendation},
			expectCount:       6,
			expectDirection:   sdk.ScaleDirectionNone,
			expectNoOp:        true,
			expectMismatch:    false,
			expectReasonEmpty: true,
		},

		// H. Reason content.
		{
			name:              "H1 unchanged request and agreeing direction have empty reason",
			opts:              Options{Current: 6, Desired: 7, Min: nil, Max: nil, Direction: sdk.ScaleDirectionUp},
			expectCount:       7,
			expectDirection:   sdk.ScaleDirectionUp,
			expectReasonEmpty: true,
		},
		{
			name:              "H2 clamp reason names the bound value",
			opts:              Options{Current: 6, Desired: 12, Min: ptr64(2), Max: ptr64(8), Direction: sdk.ScaleDirectionUp},
			expectCount:       8,
			expectDirection:   sdk.ScaleDirectionUp,
			expectCapped:      true,
			expectReasonEmpty: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := Apply(tc.opts)

			if tc.expectErr {
				must.Error(t, err)
				must.Eq(t, Result{}, res)
				return
			}

			must.NoError(t, err)
			must.Eq(t, tc.opts.Current, res.Current)
			must.Eq(t, tc.expectCount, res.Count)
			must.Eq(t, tc.expectDirection, res.Direction)
			must.Eq(t, tc.expectCapped, res.Capped)
			must.Eq(t, tc.expectNoOp, res.NoOp)
			must.Eq(t, tc.expectMismatch, res.DirectionMismatch)
			if tc.expectReasonEmpty {
				must.Eq(t, "", res.Reason)
			} else {
				must.NotEq(t, "", res.Reason)
			}
		})
	}
}

func TestApply_ReasonText(t *testing.T) {

	t.Run("clamp above max names both counts", func(t *testing.T) {
		res, err := Apply(Options{
			Current: 6, Desired: 12, Min: ptr64(2), Max: ptr64(8),
			Direction: sdk.ScaleDirectionUp,
		})
		must.NoError(t, err)
		must.True(t, res.Capped)
		must.StrContains(t, res.Reason, "desired count 12 is above target max 8")
	})

	t.Run("clamp below min names both counts", func(t *testing.T) {
		res, err := Apply(Options{
			Current: 6, Desired: 1, Min: ptr64(2), Max: ptr64(10),
			Direction: sdk.ScaleDirectionDown,
		})
		must.NoError(t, err)
		must.True(t, res.Capped)
		must.StrContains(t, res.Reason, "desired count 1 is below target min 2")
	})

	t.Run("mismatch reason names the effective direction", func(t *testing.T) {
		res, err := Apply(Options{
			Current: 6, Desired: 3, Direction: sdk.ScaleDirectionUp,
		})
		must.NoError(t, err)
		must.True(t, res.DirectionMismatch)
		must.StrContains(t, res.Reason, "requested direction up disagrees with effective direction down")
	})

	t.Run("reversed bounds error names both values", func(t *testing.T) {
		_, err := Apply(Options{
			Current: 5, Desired: 7, Min: ptr64(10), Max: ptr64(2),
		})
		must.Error(t, err)
		must.StrContains(t, err.Error(), "min bound 10 is greater than max bound 2")
	})
}

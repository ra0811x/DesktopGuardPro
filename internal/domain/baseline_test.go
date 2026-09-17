package domain

import (
	"errors"
	"reflect"
	"testing"
)

func TestCompareAssetBaselinesClassifiesAddedRemovedAndChangedAssets(t *testing.T) {
	t.Parallel()

	before := AssetBaseline{Assets: []Asset{
		{Category: AssetCategorySoftware, Identifier: "app-a", DisplayName: "应用 A", Attributes: map[string]string{"version": "1.0", "publisher": "Contoso"}},
		{Category: AssetCategoryDevice, Identifier: "usb-old", DisplayName: "旧存储设备"},
	}}
	after := AssetBaseline{Assets: []Asset{
		{Category: AssetCategorySoftware, Identifier: "app-a", DisplayName: "应用 A", Attributes: map[string]string{"version": "2.0", "publisher": "Contoso"}},
		{Category: AssetCategoryNetwork, Identifier: "adapter-1", DisplayName: "以太网"},
	}}

	differences, err := CompareAssetBaselines(before, after)
	if err != nil {
		t.Fatalf("CompareAssetBaselines() error = %v", err)
	}

	if len(differences) != 3 {
		t.Fatalf("difference count = %d, want 3", len(differences))
	}
	if differences[0].Kind != AssetDifferenceRemoved || differences[0].Before.Identifier != "usb-old" || differences[0].After != nil {
		t.Fatalf("removed difference = %#v", differences[0])
	}
	if differences[1].Kind != AssetDifferenceAdded || differences[1].After.Identifier != "adapter-1" || differences[1].Before != nil {
		t.Fatalf("added difference = %#v", differences[1])
	}
	if differences[2].Kind != AssetDifferenceChanged || differences[2].After.Identifier != "app-a" {
		t.Fatalf("changed difference = %#v", differences[2])
	}
	if !reflect.DeepEqual(differences[2].ChangedAttributes, []string{"version"}) {
		t.Fatalf("changed attributes = %v, want [version]", differences[2].ChangedAttributes)
	}
}

func TestCompareAssetBaselinesProducesStableOrdering(t *testing.T) {
	t.Parallel()

	before := AssetBaseline{Assets: []Asset{
		{Category: AssetCategorySystem, Identifier: "timezone", DisplayName: "时区", Attributes: map[string]string{"value": "UTC"}},
		{Category: AssetCategorySoftware, Identifier: "tool-b", DisplayName: "工具 B"},
	}}
	after := AssetBaseline{Assets: []Asset{
		{Category: AssetCategorySoftware, Identifier: "tool-b", DisplayName: "工具 B", Attributes: map[string]string{"version": "1"}},
		{Category: AssetCategorySystem, Identifier: "timezone", DisplayName: "时区", Attributes: map[string]string{"value": "Asia/Shanghai"}},
	}}

	first, err := CompareAssetBaselines(before, after)
	if err != nil {
		t.Fatalf("first CompareAssetBaselines() error = %v", err)
	}
	second, err := CompareAssetBaselines(before, after)
	if err != nil {
		t.Fatalf("second CompareAssetBaselines() error = %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("differences are not stable: first = %#v, second = %#v", first, second)
	}
	if first[0].After.Identifier != "tool-b" || first[1].After.Identifier != "timezone" {
		t.Fatalf("difference ordering = %#v", first)
	}
}

func TestCompareAssetBaselinesClassifiesDevicePresence(t *testing.T) {
	before := AssetBaseline{Assets: []Asset{{Category: AssetCategoryDevice, Identifier: "old", DisplayName: "旧设备"}}}
	after := AssetBaseline{Assets: []Asset{{Category: AssetCategoryDevice, Identifier: "new", DisplayName: "新设备"}}}
	differences, err := CompareAssetBaselines(before, after)
	if err != nil || len(differences) != 2 {
		t.Fatalf("differences=%#v error=%v", differences, err)
	}
	if differences[0].PresenceStatus != "newly_discovered" || differences[1].PresenceStatus != "missing_at_session_end" {
		t.Fatalf("device presence classifications = %#v", differences)
	}
}

func TestCompareAssetBaselinesReturnsNoDifferenceForEqualAssets(t *testing.T) {
	t.Parallel()

	baseline := AssetBaseline{Assets: []Asset{{
		Category: AssetCategoryAccount, Identifier: "S-1-5-21-1", DisplayName: "Raymond",
		Attributes: map[string]string{"role": "administrator"},
	}}}

	differences, err := CompareAssetBaselines(baseline, baseline)
	if err != nil {
		t.Fatalf("CompareAssetBaselines() error = %v", err)
	}
	if len(differences) != 0 {
		t.Fatalf("differences = %#v, want none", differences)
	}
}

func TestAssetBaselineValidateRejectsInvalidAndDuplicateAssets(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		baseline AssetBaseline
		wantErr  error
	}{
		{name: "category", baseline: AssetBaseline{Assets: []Asset{{Category: "unknown", Identifier: "item", DisplayName: "项目"}}}, wantErr: ErrInvalidAssetCategory},
		{name: "identifier", baseline: AssetBaseline{Assets: []Asset{{Category: AssetCategoryDevice, Identifier: " ", DisplayName: "设备"}}}, wantErr: ErrAssetIdentifierRequired},
		{name: "display name", baseline: AssetBaseline{Assets: []Asset{{Category: AssetCategoryDevice, Identifier: "device", DisplayName: " "}}}, wantErr: ErrAssetDisplayNameRequired},
		{name: "duplicate", baseline: AssetBaseline{Assets: []Asset{{Category: AssetCategoryDevice, Identifier: "device", DisplayName: "设备"}, {Category: AssetCategoryDevice, Identifier: "device", DisplayName: "设备副本"}}}, wantErr: ErrDuplicateBaselineAsset},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.baseline.Validate()
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Validate() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestBaselineCaptureResultReturnsExpectedStartDecision(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		result      BaselineCaptureResult
		wantStatus  BaselineCaptureStatus
		wantChoice  bool
		canContinue bool
		canCancel   bool
	}{
		{
			name:        "complete success",
			result:      BaselineCaptureResult{AttemptedItemCount: 3, SucceededItemCount: 3},
			wantStatus:  BaselineCaptureStatusSucceeded,
			canContinue: true,
		},
		{
			name: "partial failure",
			result: BaselineCaptureResult{
				AttemptedItemCount: 3,
				SucceededItemCount: 2,
				Failures:           []BaselineCaptureFailure{{Item: "Wi-Fi SSID", Reason: "access denied"}},
			},
			wantStatus:  BaselineCaptureStatusPartialFailure,
			wantChoice:  true,
			canContinue: true,
			canCancel:   true,
		},
		{
			name: "complete failure",
			result: BaselineCaptureResult{
				AttemptedItemCount: 2,
				Failures: []BaselineCaptureFailure{
					{Item: "network", Reason: "service unavailable"},
					{Item: "devices", Reason: "service unavailable"},
				},
			},
			wantStatus: BaselineCaptureStatusFailed,
			wantChoice: true,
			canCancel:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			decision, err := tt.result.StartDecision()
			if err != nil {
				t.Fatalf("StartDecision() error = %v", err)
			}
			if decision.Status != tt.wantStatus ||
				decision.RequiresUserChoice != tt.wantChoice ||
				decision.CanContinue != tt.canContinue ||
				decision.CanCancel != tt.canCancel {
				t.Fatalf("decision = %#v", decision)
			}
		})
	}
}

func TestBaselineCaptureResultRejectsInconsistentData(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		result  BaselineCaptureResult
		wantErr error
	}{
		{
			name:    "negative attempt count",
			result:  BaselineCaptureResult{AttemptedItemCount: -1},
			wantErr: ErrInvalidBaselineAttemptedItemCount,
		},
		{
			name:    "success exceeds attempted",
			result:  BaselineCaptureResult{AttemptedItemCount: 1, SucceededItemCount: 2},
			wantErr: ErrInvalidBaselineSucceededItemCount,
		},
		{
			name: "failure count does not match totals",
			result: BaselineCaptureResult{
				AttemptedItemCount: 2,
				SucceededItemCount: 1,
				Failures:           []BaselineCaptureFailure{},
			},
			wantErr: ErrInconsistentBaselineFailureCount,
		},
		{
			name: "failure reason missing",
			result: BaselineCaptureResult{
				AttemptedItemCount: 1,
				Failures:           []BaselineCaptureFailure{{Item: "network"}},
			},
			wantErr: ErrBaselineFailureReasonRequired,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if _, err := tt.result.StartDecision(); !errors.Is(err, tt.wantErr) {
				t.Fatalf("StartDecision() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

package domain

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

var (
	ErrInvalidAssetCategory              = errors.New("invalid asset category")
	ErrAssetIdentifierRequired           = errors.New("asset identifier is required")
	ErrAssetDisplayNameRequired          = errors.New("asset display name is required")
	ErrDuplicateBaselineAsset            = errors.New("duplicate baseline asset")
	ErrInvalidBaselineAttemptedItemCount = errors.New("invalid baseline attempted item count")
	ErrInvalidBaselineSucceededItemCount = errors.New("invalid baseline succeeded item count")
	ErrInconsistentBaselineFailureCount  = errors.New("inconsistent baseline failure count")
	ErrBaselineFailureItemRequired       = errors.New("baseline failure item is required")
	ErrBaselineFailureReasonRequired     = errors.New("baseline failure reason is required")
)

type BaselineCaptureStatus string

const (
	BaselineCaptureStatusSucceeded      BaselineCaptureStatus = "succeeded"
	BaselineCaptureStatusPartialFailure BaselineCaptureStatus = "partial_failure"
	BaselineCaptureStatusFailed         BaselineCaptureStatus = "failed"
)

type BaselineCaptureFailure struct {
	Item   string `json:"item"`
	Reason string `json:"reason"`
}

type BaselineCaptureResult struct {
	AttemptedItemCount int                      `json:"attemptedItemCount"`
	SucceededItemCount int                      `json:"succeededItemCount"`
	Failures           []BaselineCaptureFailure `json:"failures,omitempty"`
}

type BaselineStartDecision struct {
	Status             BaselineCaptureStatus    `json:"status"`
	Failures           []BaselineCaptureFailure `json:"failures,omitempty"`
	RequiresUserChoice bool                     `json:"requiresUserChoice"`
	CanContinue        bool                     `json:"canContinue"`
	CanCancel          bool                     `json:"canCancel"`
}

func (result BaselineCaptureResult) StartDecision() (BaselineStartDecision, error) {
	if err := result.Validate(); err != nil {
		return BaselineStartDecision{}, err
	}

	decision := BaselineStartDecision{
		Failures: append([]BaselineCaptureFailure(nil), result.Failures...),
	}
	switch {
	case len(result.Failures) == 0:
		decision.Status = BaselineCaptureStatusSucceeded
		decision.CanContinue = true
	case result.SucceededItemCount == 0:
		decision.Status = BaselineCaptureStatusFailed
		decision.RequiresUserChoice = true
		decision.CanCancel = true
	default:
		decision.Status = BaselineCaptureStatusPartialFailure
		decision.RequiresUserChoice = true
		decision.CanContinue = true
		decision.CanCancel = true
	}
	return decision, nil
}

func (result BaselineCaptureResult) Validate() error {
	if result.AttemptedItemCount < 0 {
		return ErrInvalidBaselineAttemptedItemCount
	}
	if result.SucceededItemCount < 0 || result.SucceededItemCount > result.AttemptedItemCount {
		return ErrInvalidBaselineSucceededItemCount
	}
	if len(result.Failures) != result.AttemptedItemCount-result.SucceededItemCount {
		return ErrInconsistentBaselineFailureCount
	}
	for _, failure := range result.Failures {
		if strings.TrimSpace(failure.Item) == "" {
			return ErrBaselineFailureItemRequired
		}
		if strings.TrimSpace(failure.Reason) == "" {
			return ErrBaselineFailureReasonRequired
		}
	}
	return nil
}

type AssetCategory string

const (
	AssetCategorySoftware AssetCategory = "software"
	AssetCategoryDevice   AssetCategory = "device"
	AssetCategoryNetwork  AssetCategory = "network"
	AssetCategoryAccount  AssetCategory = "account"
	AssetCategorySystem   AssetCategory = "system"
)

type Asset struct {
	Category    AssetCategory     `json:"category"`
	Identifier  string            `json:"identifier"`
	DisplayName string            `json:"displayName"`
	Attributes  map[string]string `json:"attributes,omitempty"`
}

type AssetBaseline struct {
	Assets []Asset `json:"assets"`
}

type AssetDifferenceKind string

const (
	AssetDifferenceAdded   AssetDifferenceKind = "added"
	AssetDifferenceRemoved AssetDifferenceKind = "removed"
	AssetDifferenceChanged AssetDifferenceKind = "changed"
)

type AssetDifference struct {
	Kind              AssetDifferenceKind `json:"kind"`
	PresenceStatus    string              `json:"presenceStatus,omitempty"`
	Before            *Asset              `json:"before,omitempty"`
	After             *Asset              `json:"after,omitempty"`
	ChangedAttributes []string            `json:"changedAttributes,omitempty"`
}

func (baseline AssetBaseline) Validate() error {
	assets := make(map[string]struct{}, len(baseline.Assets))
	for _, asset := range baseline.Assets {
		if err := asset.Validate(); err != nil {
			return err
		}
		key := asset.key()
		if _, exists := assets[key]; exists {
			return fmt.Errorf("%w: %s", ErrDuplicateBaselineAsset, key)
		}
		assets[key] = struct{}{}
	}
	return nil
}

func (asset Asset) Validate() error {
	if !asset.Category.valid() {
		return ErrInvalidAssetCategory
	}
	if strings.TrimSpace(asset.Identifier) == "" {
		return ErrAssetIdentifierRequired
	}
	if strings.TrimSpace(asset.DisplayName) == "" {
		return ErrAssetDisplayNameRequired
	}
	return nil
}

func CompareAssetBaselines(before, after AssetBaseline) ([]AssetDifference, error) {
	if err := before.Validate(); err != nil {
		return nil, fmt.Errorf("validate prior baseline: %w", err)
	}
	if err := after.Validate(); err != nil {
		return nil, fmt.Errorf("validate current baseline: %w", err)
	}

	previous := baselineAssetIndex(before)
	current := baselineAssetIndex(after)
	keys := make(map[string]struct{}, len(previous)+len(current))
	for key := range previous {
		keys[key] = struct{}{}
	}
	for key := range current {
		keys[key] = struct{}{}
	}

	orderedKeys := make([]string, 0, len(keys))
	for key := range keys {
		orderedKeys = append(orderedKeys, key)
	}
	sort.Strings(orderedKeys)

	differences := make([]AssetDifference, 0, len(orderedKeys))
	for _, key := range orderedKeys {
		previousAsset, existedBefore := previous[key]
		currentAsset, existsAfter := current[key]
		switch {
		case !existedBefore && existsAfter:
			asset := cloneAsset(currentAsset)
			difference := AssetDifference{Kind: AssetDifferenceAdded, After: &asset}
			if asset.Category == AssetCategoryDevice {
				difference.PresenceStatus = "newly_discovered"
			}
			differences = append(differences, difference)
		case existedBefore && !existsAfter:
			asset := cloneAsset(previousAsset)
			difference := AssetDifference{Kind: AssetDifferenceRemoved, Before: &asset}
			if asset.Category == AssetCategoryDevice {
				difference.PresenceStatus = "missing_at_session_end"
			}
			differences = append(differences, difference)
		case existedBefore && existsAfter:
			changedAttributes := assetChangedAttributes(previousAsset, currentAsset)
			if len(changedAttributes) == 0 {
				continue
			}
			previousCopy := cloneAsset(previousAsset)
			currentCopy := cloneAsset(currentAsset)
			differences = append(differences, AssetDifference{
				Kind: AssetDifferenceChanged, Before: &previousCopy, After: &currentCopy,
				ChangedAttributes: changedAttributes,
			})
		}
	}
	return differences, nil
}

func (category AssetCategory) valid() bool {
	switch category {
	case AssetCategorySoftware, AssetCategoryDevice, AssetCategoryNetwork, AssetCategoryAccount, AssetCategorySystem:
		return true
	default:
		return false
	}
}

func (asset Asset) key() string {
	return string(asset.Category) + "\x00" + strings.TrimSpace(asset.Identifier)
}

func baselineAssetIndex(baseline AssetBaseline) map[string]Asset {
	assets := make(map[string]Asset, len(baseline.Assets))
	for _, asset := range baseline.Assets {
		assets[asset.key()] = asset
	}
	return assets
}

func assetChangedAttributes(before, after Asset) []string {
	changed := make(map[string]struct{})
	if before.DisplayName != after.DisplayName {
		changed["displayName"] = struct{}{}
	}
	for name, beforeValue := range before.Attributes {
		if afterValue, exists := after.Attributes[name]; !exists || afterValue != beforeValue {
			changed[name] = struct{}{}
		}
	}
	for name := range after.Attributes {
		if _, exists := before.Attributes[name]; !exists {
			changed[name] = struct{}{}
		}
	}
	attributes := make([]string, 0, len(changed))
	for name := range changed {
		attributes = append(attributes, name)
	}
	sort.Strings(attributes)
	return attributes
}

func cloneAsset(asset Asset) Asset {
	copy := asset
	if len(asset.Attributes) == 0 {
		return copy
	}
	copy.Attributes = make(map[string]string, len(asset.Attributes))
	for name, value := range asset.Attributes {
		copy.Attributes[name] = value
	}
	return copy
}

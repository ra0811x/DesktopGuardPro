package domain

import (
	"errors"
	"path/filepath"
	"strings"
)

var (
	ErrMonitoringTargetPathRequired = errors.New("monitoring target path is required")
	ErrMonitoringTargetKind         = errors.New("monitoring target kind is invalid")
	ErrMonitoringTargetRecursion    = errors.New("monitoring target recursion is invalid")
	ErrDuplicateMonitoringTarget    = errors.New("duplicate monitoring target")
	ErrTooManyMonitoringTargets     = errors.New("too many monitoring targets")
	ErrMonitoringExclusionKind      = errors.New("monitoring exclusion kind is invalid")
	ErrMonitoringExclusionPattern   = errors.New("monitoring exclusion pattern is invalid")
	ErrDuplicateMonitoringExclusion = errors.New("duplicate monitoring exclusion")
)

const MaximumMonitoringTargets = 16
const MaximumMonitoringExclusions = 64

type MonitoringTargetKind string

type MonitoringTargetResolutionStatus string

const (
	MonitoringTargetKindDirectory             MonitoringTargetKind             = "directory"
	MonitoringTargetKindFile                  MonitoringTargetKind             = "file"
	MonitoringTargetKindRemovableVolume       MonitoringTargetKind             = "removable_volume"
	MonitoringTargetResolutionAvailable       MonitoringTargetResolutionStatus = "available"
	MonitoringTargetResolutionReparseResolved MonitoringTargetResolutionStatus = "reparse_resolved"
)

type MonitoringTarget struct {
	Path             string                           `json:"path"`
	Kind             MonitoringTargetKind             `json:"kind"`
	Recursive        bool                             `json:"recursive"`
	ConfiguredPath   string                           `json:"configuredPath,omitempty"`
	ResolutionStatus MonitoringTargetResolutionStatus `json:"resolutionStatus,omitempty"`
	ResolutionDetail string                           `json:"resolutionDetail,omitempty"`
}

type MonitoringExclusionKind string

const (
	MonitoringExclusionKindPath      MonitoringExclusionKind = "path"
	MonitoringExclusionKindFileName  MonitoringExclusionKind = "file_name"
	MonitoringExclusionKindExtension MonitoringExclusionKind = "extension"
	MonitoringExclusionKindProcess   MonitoringExclusionKind = "process"
)

type MonitoringExclusion struct {
	Kind    MonitoringExclusionKind `json:"kind"`
	Pattern string                  `json:"pattern"`
}

func ValidateMonitoringTargets(targets []MonitoringTarget) error {
	if len(targets) > MaximumMonitoringTargets {
		return ErrTooManyMonitoringTargets
	}
	seen := make(map[string]struct{}, len(targets))
	for _, target := range targets {
		if strings.TrimSpace(target.Path) == "" {
			return ErrMonitoringTargetPathRequired
		}
		if !target.Kind.valid() {
			return ErrMonitoringTargetKind
		}
		if target.Recursive && target.Kind != MonitoringTargetKindDirectory {
			return ErrMonitoringTargetRecursion
		}
		key := string(target.Kind) + "\x00" + canonicalMonitoringTargetPath(target.Path)
		if _, exists := seen[key]; exists {
			return ErrDuplicateMonitoringTarget
		}
		seen[key] = struct{}{}
	}
	return nil
}

func CloneMonitoringTargets(targets []MonitoringTarget) []MonitoringTarget {
	return append([]MonitoringTarget(nil), targets...)
}

func ValidateMonitoringExclusions(exclusions []MonitoringExclusion) error {
	if len(exclusions) > MaximumMonitoringExclusions {
		return ErrTooManyMonitoringTargets
	}
	seen := make(map[string]struct{}, len(exclusions))
	for _, exclusion := range exclusions {
		pattern := strings.TrimSpace(exclusion.Pattern)
		if !exclusion.Kind.valid() {
			return ErrMonitoringExclusionKind
		}
		if !validMonitoringExclusionPattern(exclusion.Kind, pattern) {
			return ErrMonitoringExclusionPattern
		}
		key := string(exclusion.Kind) + "\x00" + strings.ToLower(pattern)
		if _, exists := seen[key]; exists {
			return ErrDuplicateMonitoringExclusion
		}
		seen[key] = struct{}{}
	}
	return nil
}

func MatchesMonitoringExclusion(exclusions []MonitoringExclusion, path, process string) bool {
	path = canonicalMonitoringTargetPath(path)
	fileName := strings.ToLower(filepath.Base(path))
	extension := strings.ToLower(filepath.Ext(path))
	processName := strings.ToLower(filepath.Base(strings.TrimSpace(process)))
	for _, exclusion := range exclusions {
		pattern := strings.ToLower(strings.TrimSpace(exclusion.Pattern))
		switch exclusion.Kind {
		case MonitoringExclusionKindPath:
			pattern = canonicalMonitoringTargetPath(pattern)
			if path == pattern || strings.HasPrefix(path, pattern+`\`) {
				return true
			}
		case MonitoringExclusionKindFileName:
			if fileName == pattern {
				return true
			}
		case MonitoringExclusionKindExtension:
			if extension == pattern {
				return true
			}
		case MonitoringExclusionKindProcess:
			if processName == pattern {
				return true
			}
		}
	}
	return false
}

func (kind MonitoringTargetKind) valid() bool {
	return kind == MonitoringTargetKindDirectory || kind == MonitoringTargetKindFile || kind == MonitoringTargetKindRemovableVolume
}

func (kind MonitoringExclusionKind) valid() bool {
	return kind == MonitoringExclusionKindPath || kind == MonitoringExclusionKindFileName ||
		kind == MonitoringExclusionKindExtension || kind == MonitoringExclusionKindProcess
}

func validMonitoringExclusionPattern(kind MonitoringExclusionKind, pattern string) bool {
	if pattern == "" {
		return false
	}
	switch kind {
	case MonitoringExclusionKindExtension:
		return strings.HasPrefix(pattern, ".") && !strings.ContainsAny(pattern, `\\/`)
	case MonitoringExclusionKindFileName, MonitoringExclusionKindProcess:
		return !strings.ContainsAny(pattern, `\\/`)
	default:
		return true
	}
}

func canonicalMonitoringTargetPath(value string) string {
	return strings.ToLower(strings.TrimRight(strings.TrimSpace(value), `\\/`))
}

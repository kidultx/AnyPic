package main

import (
	"context"
	"errors"
	"image"
	"os"
)

type OutcomeKind string

const (
	OutcomeEncoded       OutcomeKind = "encoded"
	OutcomePassedThrough OutcomeKind = "passed_through"
	OutcomeFailed        OutcomeKind = "failed"
	OutcomeCanceled      OutcomeKind = "canceled"
)

type OutcomeReason string

const (
	ReasonNone                 OutcomeReason = ""
	ReasonNotSmaller           OutcomeReason = "not_smaller"
	ReasonTargetMet            OutcomeReason = "target_met"
	ReasonPixelsChanged        OutcomeReason = "pixels_changed"
	ReasonAnimatedPNG          OutcomeReason = "animated_png"
	ReasonColorSpace           OutcomeReason = "color_space"
	ReasonCanceled             OutcomeReason = "canceled"
	ReasonUnknown              OutcomeReason = "unknown_error"
	ReasonReadOriginal         OutcomeReason = "read_original"
	ReasonDecodeCorrupt        OutcomeReason = "decode_corrupt"
	ReasonEncodeJPEG           OutcomeReason = "encode_jpeg"
	ReasonEncodePNG            OutcomeReason = "encode_png"
	ReasonEncodeWebP           OutcomeReason = "encode_webp"
	ReasonUnsupportedOutput    OutcomeReason = "unsupported_output"
	ReasonLosslessCheckFailed  OutcomeReason = "lossless_check_failed"
	ReasonCreateTemp           OutcomeReason = "create_temp"
	ReasonWriteResult          OutcomeReason = "write_result"
	ReasonUnsupportedAnimation OutcomeReason = "unsupported_animation"
	ReasonNoAnimationFrames    OutcomeReason = "no_animation_frames"
)

type OutcomeSnapshot struct {
	Kind               OutcomeKind     `json:"kind"`
	Reason             OutcomeReason   `json:"reason"`
	CompressedSize     int64           `json:"compressedSize"`
	OutputFormat       string          `json:"outputFormat"`
	OutputName         string          `json:"outputName"`
	Width              int             `json:"width"`
	Height             int             `json:"height"`
	HasAlpha           bool            `json:"hasAlpha"`
	Metadata           *MetadataReport `json:"metadata,omitempty"`
	TransparencyKept   bool            `json:"transparencyKept"`
	SavedPercent       float64         `json:"savedPercent"`
	SSIM               float64         `json:"ssim"`
	SSIMMeasured       bool            `json:"ssimMeasured"`
	LargeImageFastMode bool            `json:"largeImageFastMode"`
	TargetBytes        int64           `json:"targetBytes"`
	TargetQuality      int             `json:"targetQuality"`
	TargetMissed       bool            `json:"targetMissed"`
	ErrorDetail        string          `json:"errorDetail,omitempty"`
}

type Outcome struct {
	result OutcomeSnapshot
	path   string
}

func (reason OutcomeReason) passthrough() bool {
	switch reason {
	case ReasonNotSmaller, ReasonTargetMet, ReasonPixelsChanged, ReasonAnimatedPNG, ReasonColorSpace:
		return true
	default:
		return false
	}
}

func (outcome Outcome) Kind() OutcomeKind {
	if outcome.result.Kind == "" {
		return OutcomeFailed
	}
	return outcome.result.Kind
}

func (outcome Outcome) Reason() OutcomeReason {
	if outcome.result.Kind == "" {
		return ReasonUnknown
	}
	return outcome.result.Reason
}

func (outcome Outcome) Successful() bool {
	return outcome.Kind() == OutcomeEncoded || outcome.Kind() == OutcomePassedThrough
}

func (outcome Outcome) Path() string {
	return outcome.path
}

func (outcome Outcome) Snapshot() OutcomeSnapshot {
	result := outcome.result
	result.Kind = outcome.Kind()
	result.Reason = outcome.Reason()
	if result.Metadata != nil {
		metadata := *result.Metadata
		result.Metadata = &metadata
	}
	return result
}

func (outcome Outcome) Err() error {
	if outcome.Successful() {
		return nil
	}
	return compressionError{reason: outcome.Reason(), detail: outcome.result.ErrorDetail}
}

func newEncodedOutcome(path string, originalSize int64, result OutcomeSnapshot) *Outcome {
	if path == "" || result.CompressedSize <= 0 || result.Width <= 0 || result.Height <= 0 || result.OutputFormat == "" || result.OutputName == "" {
		panic("anypic: incomplete encoded outcome")
	}
	result.Kind = OutcomeEncoded
	result.Reason = ReasonNone
	result.ErrorDetail = ""
	result.SavedPercent = 0
	if originalSize > result.CompressedSize {
		result.SavedPercent = float64(originalSize-result.CompressedSize) / float64(originalSize) * 100
	}
	return newOutcome(path, result)
}

func newPassedThroughOutcome(path, name, format string, settings Settings, reason OutcomeReason, result OutcomeSnapshot) *Outcome {
	if !reason.passthrough() {
		panic("anypic: passthrough requires a valid reason")
	}
	info, err := os.Stat(path)
	if err != nil {
		return newFailedOutcome(compressionError{reason: ReasonReadOriginal})
	}
	if result.Width <= 0 || result.Height <= 0 {
		file, err := os.Open(path)
		if err != nil {
			return newFailedOutcome(compressionError{reason: ReasonReadOriginal})
		}
		config, _, err := image.DecodeConfig(file)
		file.Close()
		if err != nil {
			return newFailedOutcome(compressionError{reason: ReasonDecodeCorrupt})
		}
		result.Width, result.Height = config.Width, config.Height
		result.HasAlpha = displayFormat(format) == "PNG" && pngHasAlpha(path)
	}
	result.Kind = OutcomePassedThrough
	result.Reason = reason
	result.CompressedSize = info.Size()
	result.OutputFormat = displayFormat(format)
	result.OutputName = outputFilename(name, settings.Suffix, extForFormat(format))
	result.Metadata = nil
	result.SavedPercent = 0
	result.SSIM = 0
	if result.SSIMMeasured {
		result.SSIM = 1
	}
	result.LargeImageFastMode = false
	result.TargetBytes = max(0, settings.TargetSize)
	result.TargetQuality = 0
	result.TargetMissed = result.TargetBytes > 0 && info.Size() > result.TargetBytes
	result.ErrorDetail = ""
	return newOutcome(path, result)
}

func newFailedOutcome(err error) *Outcome {
	if err == nil {
		panic("anypic: failed outcome requires an error")
	}
	if errors.Is(err, context.Canceled) {
		return newOutcome("", OutcomeSnapshot{Kind: OutcomeCanceled, Reason: ReasonCanceled})
	}
	var failure compressionError
	if errors.As(err, &failure) {
		return newOutcome("", OutcomeSnapshot{Kind: OutcomeFailed, Reason: failure.reason, ErrorDetail: failure.detail})
	}
	return newOutcome("", OutcomeSnapshot{Kind: OutcomeFailed, Reason: ReasonUnknown, ErrorDetail: err.Error()})
}

func newOutcome(path string, result OutcomeSnapshot) *Outcome {
	valid := false
	switch result.Kind {
	case OutcomeEncoded:
		valid = result.Reason == ReasonNone
	case OutcomePassedThrough:
		valid = result.Reason.passthrough()
	case OutcomeCanceled:
		valid = result.Reason == ReasonCanceled
	case OutcomeFailed:
		switch result.Reason {
		case ReasonUnknown, ReasonReadOriginal, ReasonDecodeCorrupt, ReasonEncodeJPEG, ReasonEncodePNG, ReasonEncodeWebP,
			ReasonUnsupportedOutput, ReasonLosslessCheckFailed, ReasonCreateTemp, ReasonWriteResult, ReasonUnsupportedAnimation, ReasonNoAnimationFrames:
			valid = true
		}
	}
	if !valid {
		panic("anypic: invalid outcome kind/reason")
	}
	outcome := Outcome{path: path, result: result}
	outcome.result = outcome.Snapshot()
	return &outcome
}

type compressionError struct {
	reason OutcomeReason
	detail string
}

func (failure compressionError) Error() string {
	switch failure.reason {
	case ReasonCanceled:
		return tr().GoErrCanceled()
	case ReasonReadOriginal:
		return tr().GoErrReadOriginal()
	case ReasonDecodeCorrupt:
		return tr().GoErrDecodeCorrupt()
	case ReasonEncodeJPEG:
		return tr().GoErrEncodeJPEG()
	case ReasonEncodePNG:
		return tr().GoErrEncodePNG()
	case ReasonEncodeWebP:
		return tr().GoErrEncodeWebP()
	case ReasonUnsupportedOutput:
		return tr().GoErrUnsupportedOutput()
	case ReasonLosslessCheckFailed:
		return tr().GoErrLosslessCheckFailed()
	case ReasonCreateTemp:
		return tr().GoErrCreateTemp()
	case ReasonWriteResult:
		return tr().GoErrWriteResult()
	case ReasonUnsupportedAnimation:
		return tr().GoErrUnsupportedAnimation()
	case ReasonNoAnimationFrames:
		return tr().GoErrNoAnimationFrames()
	default:
		if failure.detail != "" {
			return failure.detail
		}
		return tr().Failed()
	}
}

func (failure compressionError) Unwrap() error {
	if failure.reason == ReasonCanceled {
		return context.Canceled
	}
	return nil
}

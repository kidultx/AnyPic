import type { Messages } from "./locales";
import type { ItemSnapshot, MetadataReport, OutcomeReason, OutcomeSnapshot } from "./mygo";

const passthroughMessages = {
  not_smaller: ["keptOriginal", "keptOriginalTitle"],
  target_met: ["bestWithinTarget", "bestWithinTargetTitle"],
  pixels_changed: ["bestGuardFailed", "bestGuardFailedTitle"],
  animated_png: ["keptAnimatedPNG", "keptAnimatedPNGTitle"],
  color_space: ["keptColorSpace", "keptColorSpaceTitle"],
} as const satisfies Partial<Record<OutcomeReason, readonly [keyof Messages, keyof Messages]>>;

type PassthroughReason = keyof typeof passthroughMessages;
type FailureReason = Exclude<OutcomeReason, PassthroughReason | "" | "canceled">;

const failureMessages = {
  unknown_error: "failed",
  read_original: "goErrReadOriginal",
  decode_corrupt: "goErrDecodeCorrupt",
  encode_jpeg: "goErrEncodeJPEG",
  encode_png: "goErrEncodePNG",
  encode_webp: "goErrEncodeWebP",
  unsupported_output: "goErrUnsupportedOutput",
  lossless_check_failed: "goErrLosslessCheckFailed",
  create_temp: "goErrCreateTemp",
  write_result: "goErrWriteResult",
  unsupported_animation: "goErrUnsupportedAnimation",
  no_animation_frames: "goErrNoAnimationFrames",
} as const satisfies Record<FailureReason, keyof Messages>;

type OutcomeFacts = Readonly<Omit<OutcomeSnapshot, "kind" | "reason" | "metadata">> & {
  readonly metadata?: Readonly<MetadataReport>;
};

export type Outcome = OutcomeFacts & (
  | { readonly kind: "encoded"; readonly reason: "" }
  | { readonly kind: "passed_through"; readonly reason: PassthroughReason }
  | { readonly kind: "failed"; readonly reason: FailureReason }
  | { readonly kind: "canceled"; readonly reason: "canceled" }
);

export type Item = Omit<ItemSnapshot, "outcome"> & { outcome: Outcome | null };

export function readOutcome(snapshot: OutcomeSnapshot | null): Outcome | null {
  if (snapshot === null) return null;
  const valid =
    (snapshot.kind === "encoded" && snapshot.reason === "") ||
    (snapshot.kind === "passed_through" && Object.hasOwn(passthroughMessages, snapshot.reason)) ||
    (snapshot.kind === "failed" && Object.hasOwn(failureMessages, snapshot.reason)) ||
    (snapshot.kind === "canceled" && snapshot.reason === "canceled");
  if (!valid) throw new Error("anypic: invalid outcome kind/reason");
  return Object.freeze({
    ...snapshot,
    metadata: snapshot.metadata ? Object.freeze({ ...snapshot.metadata }) : undefined,
  }) as Outcome;
}

export function readItem(snapshot: ItemSnapshot): Item {
  return { ...snapshot, outcome: readOutcome(snapshot.outcome) };
}

export interface OutcomePresentation {
  kept: { label: string; title: string } | null;
  quality: { label: string; title: string; className: string } | null;
  target: { bytes: number; cap: number; qualityLabel: string } | null;
  error: string | null;
}

export function presentOutcome(outcome: Outcome | null, messages: Messages): OutcomePresentation {
  const display: OutcomePresentation = { kept: null, quality: null, target: null, error: null };
  if (!outcome) return display;
  switch (outcome.kind) {
    case "failed":
      display.error = outcome.reason === "unknown_error" && outcome.errorDetail
        ? outcome.errorDetail
        : messages[failureMessages[outcome.reason]]();
      return display;
    case "canceled":
      display.error = messages.goErrCanceled();
      return display;
    case "passed_through": {
      const [label, title] = passthroughMessages[outcome.reason];
      display.kept = { label: messages[label](), title: messages[title]() };
      break;
    }
    case "encoded":
      display.quality = outcome.ssimMeasured
        ? {
            label: outcome.ssim >= 0.98 ? messages.qualityNearlyIdentical()
              : outcome.ssim >= 0.92 ? messages.qualitySlightLoss() : messages.qualityVisibleLoss(),
            className: outcome.ssim >= 0.98 ? "" : outcome.ssim >= 0.92 ? "slight" : "visible",
            title: messages.ssimTitle(outcome.ssim.toFixed(3)),
          }
        : { label: messages.qualityNotMeasured(), title: messages.qualityNotMeasuredTitle(), className: "" };
  }
  if (outcome.targetMissed && outcome.targetBytes > 0) {
    display.target = {
      bytes: outcome.compressedSize,
      cap: outcome.targetBytes,
      qualityLabel: outcome.kind === "passed_through" ? messages.targetQualityOriginal()
        : outcome.targetQuality > 0 ? messages.targetQualityValue(outcome.targetQuality) : messages.targetQualityLossless(),
    };
  }
  return display;
}

import { describe, expect, test } from "bun:test";
import { messagesIn } from "../src/locales";
import type { MetadataReport, OutcomeSnapshot } from "../src/mygo";
import { presentOutcome, readOutcome } from "../src/outcome";

const messages = messagesIn("en");

function result(overrides: Partial<OutcomeSnapshot> = {}): OutcomeSnapshot {
  return {
    kind: "encoded", reason: "", compressedSize: 100, outputFormat: "PNG", outputName: "source-anypic.png",
    width: 12, height: 8, hasAlpha: false, transparencyKept: false, savedPercent: 50,
    ssim: 1, ssimMeasured: true, largeImageFastMode: false, targetBytes: 0, targetQuality: 0, targetMissed: false,
    ...overrides,
  };
}

describe("immutable outcome", () => {
  test("nil is the only uncompressed state", () => {
    expect(readOutcome(null)).toBeNull();
    expect(presentOutcome(null, messages)).toEqual({ kept: null, quality: null, target: null, error: null });
  });

  test.each([
    { kind: "passed_through", reason: "" },
    { kind: "encoded", reason: "not_smaller" },
    { kind: "failed", reason: "target_met" },
    { kind: "canceled", reason: "read_original" },
  ] as const)("rejects invalid $kind/$reason", (invalid) => {
    expect(() => readOutcome(result(invalid))).toThrow("invalid outcome");
  });

  test("snapshots and nested metadata cannot mutate the result", () => {
    const metadata: MetadataReport = {
      iccKept: true, exifKept: false, gpsRemoved: false, orientationReset: false,
      iccDroppedIncompatible: false, iccDroppedUnsupported: false, dropped: false,
    };
    const snapshot = result({ metadata });
    const outcome = readOutcome(snapshot)!;
    snapshot.outputName = "changed.png";
    metadata.iccKept = false;
    expect(outcome.outputName).toBe("source-anypic.png");
    expect(outcome.metadata?.iccKept).toBe(true);
    expect(Object.isFrozen(outcome)).toBe(true);
    expect(Object.isFrozen(outcome.metadata)).toBe(true);
    expect(() => { (outcome as OutcomeSnapshot).outputName = "changed.png"; }).toThrow();
  });
});

describe("outcome presentation", () => {
  test.each([
    ["not_smaller", messages.keptOriginal()],
    ["target_met", messages.bestWithinTarget()],
    ["pixels_changed", messages.bestGuardFailed()],
    ["animated_png", messages.keptAnimatedPNG()],
    ["color_space", messages.keptColorSpace()],
  ] as const)("passthrough says why: %s", (reason, label) => {
    const display = presentOutcome(readOutcome(result({ kind: "passed_through", reason })), messages);
    expect(display.kept?.label).toBe(label);
    expect(display.kept?.title.length).toBeGreaterThan(0);
    expect(display.quality).toBeNull();
    expect(display.error).toBeNull();
  });

  test.each([
    [0, "visible"], [0.919, "visible"], [0.92, "slight"], [0.979, "slight"], [0.98, ""], [1, ""],
  ] as const)("measured SSIM %s has tier %s", (ssim, className) => {
    const display = presentOutcome(readOutcome(result({ ssim })), messages);
    expect(display.quality?.className).toBe(className);
    expect(display.quality?.title).toBe(messages.ssimTitle(ssim.toFixed(3)));
  });

  test("an unmeasured animation never borrows a quality tier", () => {
    const display = presentOutcome(readOutcome(result({ ssim: 0, ssimMeasured: false })), messages);
    expect(display.quality?.label).toBe(messages.qualityNotMeasured());
    expect(display.quality?.title).toBe(messages.qualityNotMeasuredTitle());
  });

  test.each([
    ["passed_through", "not_smaller", 0, messages.targetQualityOriginal()],
    ["encoded", "", 0, messages.targetQualityLossless()],
    ["encoded", "", 10, messages.targetQualityValue(10)],
  ] as const)("target miss labels the actual %s quality", (kind, reason, targetQuality, qualityLabel) => {
    const outcome = readOutcome(result({ kind, reason, targetBytes: 80, targetQuality, targetMissed: true }));
    expect(presentOutcome(outcome, messages).target).toEqual({ bytes: 100, cap: 80, qualityLabel });
  });

  test("target badges require a cap and a miss", () => {
    expect(presentOutcome(readOutcome(result({ targetBytes: 80 })), messages).target).toBeNull();
    expect(presentOutcome(readOutcome(result({ targetMissed: true })), messages).target).toBeNull();
  });

  test.each([
    ["failed", "read_original", messages.goErrReadOriginal()],
    ["failed", "decode_corrupt", messages.goErrDecodeCorrupt()],
    ["canceled", "canceled", messages.goErrCanceled()],
  ] as const)("%s/%s localizes at render time", (kind, reason, english) => {
    const outcome = readOutcome(result({ kind, reason, compressedSize: 0, outputFormat: "", outputName: "" }));
    const display = presentOutcome(outcome, messages);
    expect(display.error).toBe(english);
    expect(display.kept).toBeNull();
    expect(display.quality).toBeNull();
    expect(display.target).toBeNull();
    expect(presentOutcome(outcome, messagesIn("zh-Hans")).error).not.toBe(english);
    expect(outcome?.errorDetail).toBeUndefined();
  });

  test("unknown failures retain an untranslated diagnostic", () => {
    const outcome = readOutcome(result({ kind: "failed", reason: "unknown_error", errorDetail: "encoder diagnostic" }));
    expect(presentOutcome(outcome, messages).error).toBe("encoder diagnostic");
  });
});

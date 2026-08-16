export type Segment = {
  id: string;
  startMs: number;
  durationMs: number;
  videoCodec: string;
  audioCodec: string;
  width: number;
  height: number;
  fps: number;
  hasThumbnail: boolean;
};

export function targetSegment(segments: Segment[], targetMs: number) {
  const exact = segments.find(
    (segment) =>
      targetMs >= segment.startMs &&
      targetMs < segment.startMs + segment.durationMs,
  );
  if (exact) {
    return {
      segment: exact,
      offsetMs: targetMs - exact.startMs,
      adjusted: false,
    };
  }
  const next = segments.find((segment) => segment.startMs > targetMs);
  if (next) return { segment: next, offsetMs: 0, adjusted: true };
  const previous = segments.at(-1);
  return previous
    ? {
        segment: previous,
        offsetMs: Math.max(0, previous.durationMs - 250),
        adjusted: true,
      }
    : null;
}

export function timelineSecondsAtClientX(
  clientX: number,
  trackLeft: number,
  trackWidth: number,
) {
  if (trackWidth <= 0) return 0;
  const ratio = Math.max(0, Math.min(1, (clientX - trackLeft) / trackWidth));
  return Math.round(ratio * 86400);
}

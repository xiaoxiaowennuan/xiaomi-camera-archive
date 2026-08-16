import { describe, expect, it } from 'vitest';
import {
  targetSegment,
  timelineSecondsAtClientX,
  type Segment,
} from './timeline';

const segment = (id: string, startMs: number): Segment => ({
  id,
  startMs,
  durationMs: 60000,
  videoCodec: 'hevc',
  audioCodec: 'pcm_alaw',
  width: 2560,
  height: 1440,
  fps: 10,
  hasThumbnail: true,
});

describe('targetSegment', () => {
  const segments = [segment('a', 1000), segment('b', 121000)];

  it('seeks inside a segment', () =>
    expect(targetSegment(segments, 31000)?.offsetMs).toBe(30000));

  it('jumps forward across a gap', () =>
    expect(targetSegment(segments, 70000)?.segment.id).toBe('b'));

  it('uses the previous end after the last segment', () =>
    expect(targetSegment(segments, 999000)?.segment.id).toBe('b'));
});

describe('timelineSecondsAtClientX', () => {
  it('maps a tap position to the 24 hour timeline', () =>
    expect(timelineSecondsAtClientX(150, 100, 1200)).toBe(3600));

  it('clamps taps outside the track', () => {
    expect(timelineSecondsAtClientX(50, 100, 1200)).toBe(0);
    expect(timelineSecondsAtClientX(1400, 100, 1200)).toBe(86400);
  });
});

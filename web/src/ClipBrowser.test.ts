import { expect, it } from 'vitest';
import { hourGroups, clipTime } from './ClipBrowser';
import type { Segment } from './timeline';

it('groups by Shanghai hour, retains midnight overlaps and excludes other dates', () => {
  const day = Date.parse('2024-01-02T00:00:00+08:00');
  const sample = (id: string, seconds: number): Segment => ({ id, startMs: day + seconds * 1000,
    durationMs: 342000, videoCodec: 'hevc', audioCodec: 'opus', width: 3840, height: 2160, fps: 20, hasThumbnail: false });
  const groups = hourGroups([sample('later', 3660), sample('cross-hour', 3590), sample('first', 3600), sample('midnight', -60), sample('yesterday', -400), sample('tomorrow', 86400)], day);
  expect(groups[0].map(x => x.id)).toEqual(['midnight', 'cross-hour']);
  expect(groups[1].map(x => x.id)).toEqual(['first', 'later']);
  expect(groups.flat()).toHaveLength(4);
  expect(clipTime(day + 3590000)).toBe('00:59');
});

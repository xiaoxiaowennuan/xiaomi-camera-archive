import { describe, expect, it } from 'vitest';
import { calendarDays, monthBounds, shiftMonth } from './calendar';

describe('calendar helpers', () => {
  it('computes leap-month bounds', () => {
    expect(monthBounds('2024-02')).toMatchObject({
      from: '2024-02-01',
      to: '2024-02-29',
    });
  });

  it('builds a stable six-week calendar grid', () => {
    const days = calendarDays('2024-01');
    expect(days).toHaveLength(42);
    expect(days[0]).toMatchObject({ date: '2023-12-31', inMonth: false });
    expect(days[1]).toMatchObject({ date: '2024-01-01', inMonth: true });
    expect(days.at(-1)).toMatchObject({ date: '2024-02-10', inMonth: false });
  });

  it('moves between years', () => {
    expect(shiftMonth('2024-01', -1)).toBe('2023-12');
    expect(shiftMonth('2024-12', 1)).toBe('2025-01');
  });
});

export type CalendarDay = {
  date: string;
  day: number;
  inMonth: boolean;
};

export function formatDate(year: number, monthIndex: number, day: number) {
  return `${year}-${String(monthIndex + 1).padStart(2, '0')}-${String(day).padStart(2, '0')}`;
}

export function monthBounds(month: string) {
  const [year, number] = month.split('-').map(Number);
  const monthIndex = number - 1;
  const lastDay = new Date(Date.UTC(year, monthIndex + 1, 0)).getUTCDate();
  return {
    from: formatDate(year, monthIndex, 1),
    to: formatDate(year, monthIndex, lastDay),
    year,
    monthIndex,
  };
}

export function shiftMonth(month: string, amount: number) {
  const { year, monthIndex } = monthBounds(month);
  const shifted = new Date(Date.UTC(year, monthIndex + amount, 1));
  return formatDate(shifted.getUTCFullYear(), shifted.getUTCMonth(), 1).slice(0, 7);
}

export function calendarDays(month: string): CalendarDay[] {
  const { year, monthIndex } = monthBounds(month);
  const firstWeekday = new Date(Date.UTC(year, monthIndex, 1)).getUTCDay();
  return Array.from({ length: 42 }, (_, index) => {
    const date = new Date(Date.UTC(year, monthIndex, index - firstWeekday + 1));
    return {
      date: formatDate(date.getUTCFullYear(), date.getUTCMonth(), date.getUTCDate()),
      day: date.getUTCDate(),
      inMonth: date.getUTCMonth() === monthIndex,
    };
  });
}

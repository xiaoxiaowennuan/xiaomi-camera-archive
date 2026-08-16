import { useEffect, useMemo, useRef, useState } from 'react';
import { calendarDays, monthBounds, shiftMonth } from './calendar';

type DaySummary = {
  date: string;
  segments: number;
};

type CalendarState = {
  month: string;
  available: Set<string>;
  loading: boolean;
  error: boolean;
};

type DatePickerProps = {
  folderId: string;
  value: string;
  onChange: (value: string) => void;
};

const weekdays = ['日', '一', '二', '三', '四', '五', '六'];

export function DatePicker({ folderId, value, onChange }: DatePickerProps) {
  const [open, setOpen] = useState(false);
  const [month, setMonth] = useState(value.slice(0, 7));
  const [state, setState] = useState<CalendarState>({
    month: '',
    available: new Set(),
    loading: true,
    error: false,
  });
  const root = useRef<HTMLDivElement>(null);
  const days = useMemo(() => calendarDays(month), [month]);
  const { from, to, year, monthIndex } = monthBounds(month);

  useEffect(() => {
    const controller = new AbortController();
    setState((current) => ({ ...current, loading: true, error: false }));
    fetch(`/api/v1/days?folder=${encodeURIComponent(folderId)}&from=${from}&to=${to}`, { signal: controller.signal })
      .then((response) => {
        if (!response.ok) throw new Error('days_request_failed');
        return response.json();
      })
      .then((data: { days?: DaySummary[] }) => {
        setState({
          month,
          available: new Set(
            (data.days || [])
              .filter((day) => day.segments > 0)
              .map((day) => day.date),
          ),
          loading: false,
          error: false,
        });
      })
      .catch((error: unknown) => {
        if (error instanceof DOMException && error.name === 'AbortError') return;
        setState({ month, available: new Set(), loading: false, error: true });
      });
    return () => controller.abort();
  }, [folderId, from, month, to]);

  useEffect(() => {
    function closeOnOutsideClick(event: PointerEvent) {
      if (!root.current?.contains(event.target as Node)) setOpen(false);
    }
    document.addEventListener('pointerdown', closeOnOutsideClick);
    return () => document.removeEventListener('pointerdown', closeOnOutsideClick);
  }, []);

  function selectDate(nextDate: string) {
    onChange(nextDate);
    setOpen(false);
  }

  return (
    <div className="date-picker" ref={root}>
      <span className="date-label" id="date-label">日期</span>
      <button
        className="date-trigger"
        type="button"
        aria-labelledby="date-label selected-date"
        aria-haspopup="dialog"
        aria-expanded={open}
        onClick={() => {
          setMonth(value.slice(0, 7));
          setOpen((current) => !current);
        }}
      >
        <span id="selected-date">{value.replaceAll('-', '/')}</span>
        <span aria-hidden="true">▾</span>
      </button>

      {open && (
        <div
          className="calendar-popover"
          role="dialog"
          aria-label="选择有录像的日期"
          aria-busy={state.loading}
          onKeyDown={(event) => {
            if (event.key === 'Escape') setOpen(false);
          }}
        >
          <div className="calendar-header">
            <button
              type="button"
              aria-label="上一个月"
              onClick={() => setMonth((current) => shiftMonth(current, -1))}
            >
              ‹
            </button>
            <strong>{year} 年 {monthIndex + 1} 月</strong>
            <button
              type="button"
              aria-label="下一个月"
              onClick={() => setMonth((current) => shiftMonth(current, 1))}
            >
              ›
            </button>
          </div>
          <div className="calendar-grid calendar-weekdays" aria-hidden="true">
            {weekdays.map((weekday) => <span key={weekday}>{weekday}</span>)}
          </div>
          <div className="calendar-grid" role="grid">
            {days.map((day) => {
              const loaded = state.month === month && !state.loading;
              const available = loaded && day.inMonth && state.available.has(day.date);
              const selected = day.date === value;
              const label = `${day.date.slice(0, 4)}年${Number(day.date.slice(5, 7))}月${day.day}日，${available ? '有录像' : '无录像'}`;
              return (
                <button
                  className={`${day.inMonth ? '' : 'outside-month'}${available ? ' has-recording' : ''}${selected ? ' selected-date' : ''}`}
                  key={day.date}
                  type="button"
                  role="gridcell"
                  aria-label={label}
                  aria-selected={selected}
                  disabled={!available}
                  onClick={() => selectDate(day.date)}
                >
                  {day.day}
                </button>
              );
            })}
          </div>
          {state.error && <p className="calendar-error" role="status">可用日期加载失败</p>}
        </div>
      )}
    </div>
  );
}

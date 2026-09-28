import { useMemo, useState } from 'react';
import type { Segment } from './timeline';

export function hourGroups(segments: Segment[], dayStart: number) {
  const groups = Array.from({ length: 24 }, () => [] as Segment[]);
  for (const segment of [...segments].sort((a, b) => a.startMs - b.startMs)) {
    if (segment.startMs + segment.durationMs <= dayStart) continue;
    const hour = Math.max(0, Math.floor((segment.startMs - dayStart) / 3600000));
    if (hour >= 0 && hour < 24) groups[hour].push(segment);
  }
  return groups;
}

export function clipTime(ms: number) {
  return new Intl.DateTimeFormat('zh-CN', {
    timeZone: 'Asia/Shanghai', hour: '2-digit', minute: '2-digit', hourCycle: 'h23',
  }).format(ms);
}

function Cover({ id }: { id: string }) {
  const [failed, setFailed] = useState(false);
  return <div className="clip-cover">
    {failed ? <span>封面暂不可用</span> : <img loading="lazy" decoding="async"
      src={`/api/v1/media/${id}/first-frame`} alt="录像首帧" onError={() => setFailed(true)} />}
  </div>;
}

export function ClipBrowser({ segments, dayStart, selectedId, onSelect }: {
  segments: Segment[]; dayStart: number; selectedId?: string; onSelect: (segment: Segment) => void;
}) {
  const [hour, setHour] = useState<number | null>(null);
  const groups = useMemo(() => hourGroups(segments, dayStart), [segments, dayStart]);
  const activeHours = groups.map((clips, value) => ({ clips, value })).filter(x => x.clips.length);
  return <section className="clip-browser" aria-label="录像封面导航">
    <div className="clip-heading">
      <div><p className="clip-eyebrow">按画面找录像</p><h2>{hour === null ? '选择小时' : `${String(hour).padStart(2, '0')}:00 — ${String(hour + 1).padStart(2, '0')}:00`}</h2></div>
      {hour !== null && <button onClick={() => setHour(null)}>← 全部小时</button>}
    </div>
    <p className="clip-help">{hour === null ? '先选小时，再看每段录像的首帧。' : `${groups[hour].length} 段录像 · 点击封面播放，时间为片段实际起止时间。跨午夜录像显示在 00 点。`}</p>
    {hour !== null && <nav className="hour-strip" aria-label="切换小时">{activeHours.map(({ value }) =>
      <button key={value} aria-pressed={value === hour} onClick={() => setHour(value)}>{String(value).padStart(2, '0')}:00</button>)}</nav>}
    {!segments.length && <p className="clip-empty">这一天暂无已索引录像，请更换日期或等待扫描完成。</p>}
    <div className="clip-grid">
      {hour === null ? activeHours.map(({ clips, value }) => <button className="clip-card" key={value} onClick={() => setHour(value)} aria-label={`${value} 点，${clips.length} 段录像`}>
        <Cover id={clips[0].id} /><strong>{String(value).padStart(2, '0')}:00</strong><small>{clips.length} 段录像</small>
      </button>) : groups[hour].map(segment => <button className={`clip-card${selectedId === segment.id ? ' selected' : ''}`} key={segment.id} aria-pressed={selectedId === segment.id} onClick={() => onSelect(segment)}>
        <Cover id={segment.id} /><strong>{clipTime(segment.startMs)}–{clipTime(segment.startMs + segment.durationMs)}</strong>
      </button>)}
    </div>
  </section>;
}

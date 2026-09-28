import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { createRoot } from 'react-dom/client';
import { DatePicker } from './DatePicker';
import { ClipBrowser } from './ClipBrowser';
import {
  targetSegment,
  timelineSecondsAtClientX,
  type Segment,
} from './timeline';
import './style.css';

type Event = {
  id: string;
  startMs: number;
  endMs: number;
  rawRecordCount: number;
  hasUnknownFlag: boolean;
};

function formatTimelineTime(seconds: number) {
  const clamped = Math.max(0, Math.min(86400, seconds));
  const hours = Math.floor(clamped / 3600);
  const minutes = Math.floor((clamped % 3600) / 60);
  return `${String(hours).padStart(2, '0')}:${String(minutes).padStart(2, '0')}`;
}

type PlayerPageProps = {
  folderId: string;
  folderName: string;
  firstDate: string;
};

function PlayerPage({ folderId, folderName, firstDate }: PlayerPageProps) {
  const params = new URLSearchParams(location.search);
  const [date, setDate] = useState(params.get('date') || firstDate);
  const [segments, setSegments] = useState<Segment[]>([]);
  const [events, setEvents] = useState<Event[]>([]);
  const [selected, setSelected] = useState<Segment>();
  const [speed, setSpeed] = useState(Number(params.get('speed')) || 1);
  const [timelineSeconds, setTimelineSeconds] = useState(0);
  const [notice, setNotice] = useState('点击封面开始播放');
  const video = useRef<HTMLVideoElement>(null);
  const playerSection = useRef<HTMLElement>(null);
  const scrubbing = useRef(false);
  const pointerStart = useRef<{ x: number; y: number } | null>(null);
  const forceCompat = useRef(false);
  const mediaVariant = useRef<'source' | 'compat'>('source');
  const playbackAttempt = useRef(0);
  const frameTimer = useRef<number | undefined>(undefined);
  const frameCallback = useRef<number | undefined>(undefined);
  const dayStart = useMemo(
    () => new Date(date + 'T00:00:00+08:00').getTime(),
    [date],
  );

  useEffect(() => {
    const controller = new AbortController();
    playbackAttempt.current += 1;
    stopFrameWatchdog();
    video.current?.pause();
    setSegments([]);
    setSelected(undefined);
    const start = new Date(dayStart).toISOString();
    const end = new Date(dayStart + 86400000).toISOString();
    fetch(
      `/api/v1/timeline?folder=${encodeURIComponent(folderId)}&start=${encodeURIComponent(start)}&end=${encodeURIComponent(end)}`, { signal: controller.signal },
    )
      .then((response) => response.json())
      .then((data) => {
        setSegments(data.segments || []);
        setEvents(data.events || []);
        setSelected(undefined);
        setTimelineSeconds(0);
      })
      .catch((error) => { if (error.name !== 'AbortError') setNotice('时间线加载失败'); });
    return () => { controller.abort(); playbackAttempt.current += 1; stopFrameWatchdog(); };
  }, [dayStart, folderId]);

  useEffect(() => {
    const url = new URL(location.href);
    url.searchParams.set('date', date);
    url.searchParams.set('speed', String(speed));
    history.replaceState(null, '', url);
    if (video.current) video.current.playbackRate = speed;
  }, [date, speed]);

  function stopFrameWatchdog() {
    if (frameTimer.current !== undefined) {
      window.clearTimeout(frameTimer.current);
      frameTimer.current = undefined;
    }
    if (frameCallback.current !== undefined && video.current) {
      video.current.cancelVideoFrameCallback?.(frameCallback.current);
      frameCallback.current = undefined;
    }
  }

  useEffect(() => () => stopFrameWatchdog(), []);

  function switchToCompat(segment: Segment) {
    if (!video.current || mediaVariant.current !== 'source') return;
    const offset = video.current.currentTime * 1000;
    forceCompat.current = true;
    stopFrameWatchdog();
    setNotice('未检测到可用视频画面，正在切换兼容版本…');
    void open(segment, offset, true);
  }

  function startFrameWatchdog(segment: Segment) {
    const element = video.current;
    if (
      !element ||
      mediaVariant.current !== 'source' ||
      typeof element.requestVideoFrameCallback !== 'function'
    ) return;
    stopFrameWatchdog();
    const attempt = playbackAttempt.current;
    const startTime = element.currentTime;
    let frameSeen = false;
    frameCallback.current = element.requestVideoFrameCallback(() => {
      frameSeen = true;
      stopFrameWatchdog();
    });
    frameTimer.current = window.setTimeout(() => {
      frameTimer.current = undefined;
      if (
        attempt !== playbackAttempt.current ||
        mediaVariant.current !== 'source' ||
        frameSeen ||
        document.visibilityState !== 'visible'
      ) return;
      if (!element.paused && element.currentTime > startTime + 0.5) {
        switchToCompat(segment);
      }
    }, 3500);
  }

  async function open(segment: Segment, offset = 0, compat = false) {
    stopFrameWatchdog();
    const attempt = ++playbackAttempt.current;
    setSelected(segment);
    video.current?.pause();
    setTimelineSeconds((segment.startMs - dayStart + offset) / 1000);
    if (!video.current) return;
    let useCompat = compat || forceCompat.current;
    mediaVariant.current = useCompat ? 'compat' : 'source';
    setNotice(useCompat ? '准备兼容版本…' : '正在加载录像…');
    if (useCompat) {
      let ready = false;
      try {
        for (let index = 0; index < 900; index += 1) {
          const response = await fetch(`/api/v1/media/${segment.id}/compat`, {
            method: 'POST',
          });
          if (!response.ok) throw new Error('compat_request_failed');
          const data = await response.json();
          if (attempt !== playbackAttempt.current) return;
          if (data.state === 'ready') {
            ready = true;
            break;
          }
          if (data.state === 'failed') {
            setNotice('转码失败');
            return;
          }
          await new Promise((resolve) =>
            setTimeout(resolve, data.retryAfterMs || 1000),
          );
        }
      } catch {
        if (attempt === playbackAttempt.current) setNotice('兼容版本准备失败');
        return;
      }
      if (!ready) {
        setNotice('兼容版本生成超时');
        return;
      }
    }
    if (attempt !== playbackAttempt.current) return;
    if (!useCompat) {
      frameTimer.current = window.setTimeout(() => {
        if (attempt === playbackAttempt.current && video.current?.readyState === 0) switchToCompat(segment);
      }, 12000);
    }
    video.current.src = `/api/v1/media/${segment.id}/${useCompat ? 'compat' : 'source'}`;
    video.current.onloadedmetadata = () => {
      if (attempt !== playbackAttempt.current) return;
      if (video.current) {
        video.current.currentTime = offset / 1000;
        video.current.playbackRate = speed;
        video.current.play().catch(() => setNotice('点击播放以继续'));
      }
    };
  }

  function seek(seconds: number) {
    const hit = targetSegment(segments, dayStart + seconds * 1000);
    if (!hit) {
      setNotice('当天没有录像');
      return;
    }
    setNotice(hit.adjusted ? '该时刻无录像，已跳到最近片段' : '');
    void open(hit.segment, hit.offsetMs);
  }

  function previewTimeline(seconds: number) {
    setTimelineSeconds(seconds);
    setNotice(`定位到 ${formatTimelineTime(seconds)}，松开后播放`);
  }

  function commitTimeline(seconds: number) {
    scrubbing.current = false;
    setTimelineSeconds(seconds);
    seek(seconds);
  }

  function nextEvent(delta: number) {
    const now = selected?.startMs || dayStart;
    const list = delta > 0 ? events : [...events].reverse();
    const event = list.find((item) =>
      delta > 0 ? item.startMs > now : item.startMs < now,
    );
    if (event) seek((event.startMs - dayStart) / 1000);
  }

  function selectEvent(event: Event) {
    seek((event.startMs - dayStart) / 1000);
    if (window.matchMedia('(max-width: 800px)').matches) {
      window.requestAnimationFrame(() => {
        playerSection.current?.scrollIntoView({
          behavior: window.matchMedia('(prefers-reduced-motion: reduce)').matches
            ? 'auto'
            : 'smooth',
          block: 'start',
        });
      });
    }
  }

  return (
    <main className="gallery-layout">
      <header>
        <div>
          <a className="back-link" href="/folders">← 返回录像文件夹</a>
          <p className="eyebrow">MIJIA ARCHIVE</p>
          <h1>{folderName}</h1>
        </div>
        <DatePicker folderId={folderId} value={date} onChange={setDate} />
      </header>

      <ClipBrowser key={`${folderId}:${date}`} segments={segments} dayStart={dayStart} selectedId={selected?.id} onSelect={(segment) => {
        void open(segment);
        requestAnimationFrame(() => playerSection.current?.scrollIntoView({ block: 'start', behavior: 'smooth' }));
      }} />
      <section className="player" ref={playerSection} hidden={!selected}>
        <div className="playback-title"><strong>正在查看录像</strong><button onClick={() => { video.current?.pause(); playbackAttempt.current += 1; stopFrameWatchdog(); setSelected(undefined); }}>返回封面</button></div>
        <video
          ref={video}
          playsInline
          controls
          onError={() => {
            if (!selected) return;
            if (mediaVariant.current === 'source') {
              switchToCompat(selected);
            } else {
              setNotice('兼容版本播放失败');
            }
          }}
          onLoadStart={() => setNotice('正在加载录像…')}
          onWaiting={() => setNotice('正在缓冲…')}
          onStalled={() => setNotice('正在缓冲…')}
          onCanPlay={() => setNotice('')}
          onLoadedData={() => {
            if (
              selected &&
              mediaVariant.current === 'source' &&
              video.current?.videoWidth === 0
            ) switchToCompat(selected);
          }}
          onPlaying={() => {
            setNotice('');
            if (selected) startFrameWatchdog(selected);
          }}
          onTimeUpdate={() => {
            if (video.current && selected && !scrubbing.current) {
              setTimelineSeconds(
                (selected.startMs - dayStart) / 1000 +
                  video.current.currentTime,
              );
            }
          }}
          onEnded={() => {
            const index = segments.findIndex(
              (segment) => segment.id === selected?.id,
            );
            if (segments[index + 1]) open(segments[index + 1]);
          }}
        />
        <div className="controls">
          <button disabled={!selected} onClick={() => { if (selected) switchToCompat(selected); }}>兼容播放</button>
          <button className="event-nav" onClick={() => nextEvent(-1)}>上一个事件</button>
          {[1, 2, 4].map((value) => (
            <button
              className={`speed-button${speed === value ? ' active' : ''}`}
              onClick={() => setSpeed(value)}
              key={value}
            >
              {value}x
            </button>
          ))}
          <button className="event-nav" onClick={() => nextEvent(1)}>下一个事件</button>
        </div>
        <p role="status" aria-live="polite">{notice}</p>
      </section>

      <section className="timeline" hidden={!selected} aria-labelledby="timeline-title">
        <div className="timeline-heading">
          <h2 id="timeline-title">24 小时时间轴</h2>
          <div className="legend" aria-label="时间轴颜色图例">
            <span><i className="recording-key" />录像</span>
            <span><i className="motion-key" />画面变动</span>
            <span><i className="person-key" />有人移动</span>
            <span><i className="gap-key" />无录像</span>
          </div>
        </div>
        <div className="track">
          {segments.map((segment) => (
            <i
              className="recording-range"
              key={segment.id}
              style={{
                left: `${(segment.startMs - dayStart) / 864000}%`,
                width: `${Math.max(0.15, segment.durationMs / 864000)}%`,
              }}
            />
          ))}
          {events.map((event) => (
            <i
              className={event.hasUnknownFlag ? 'person-event-range' : 'motion-event-range'}
              key={event.id}
              title={event.hasUnknownFlag ? '有人移动' : '画面变动'}
              style={{ left: `${(event.startMs - dayStart) / 864000}%` }}
            />
          ))}
          <input
            className="timeline-scrubber"
            aria-label="24 小时时间线"
            aria-valuetext={formatTimelineTime(timelineSeconds)}
            type="range"
            min="0"
            max="86400"
            step="1"
            value={timelineSeconds}
            onPointerDown={(event) => {
              scrubbing.current = true;
              pointerStart.current = { x: event.clientX, y: event.clientY };
            }}
            onChange={(event) =>
              previewTimeline(Number(event.currentTarget.value))
            }
            onPointerUp={(event) => {
              const start = pointerStart.current;
              pointerStart.current = null;
              const isTap =
                start !== null &&
                Math.hypot(event.clientX - start.x, event.clientY - start.y) < 10;
              if (isTap) {
                const bounds = event.currentTarget.getBoundingClientRect();
                commitTimeline(
                  timelineSecondsAtClientX(
                    event.clientX,
                    bounds.left,
                    bounds.width,
                  ),
                );
                return;
              }
              commitTimeline(Number(event.currentTarget.value));
            }}
            onPointerCancel={(event) => {
              pointerStart.current = null;
              commitTimeline(Number(event.currentTarget.value));
            }}
            onKeyUp={(event) => {
              if (
                event.key.startsWith('Arrow') ||
                event.key === 'Home' ||
                event.key === 'End'
              ) {
                commitTimeline(Number(event.currentTarget.value));
              }
            }}
          />
        </div>
        <div className="ticks" aria-hidden="true">
          <span>00:00</span>
          <span>06:00</span>
          <span>12:00</span>
          <span>18:00</span>
          <span>24:00</span>
        </div>
      </section>

      <aside hidden={!selected || !events.length}>
        <h2>
          事件 <small>{events.length}</small>
        </h2>
        <div className="events">
          {events.map((event) => (
            <button
              key={event.id}
              className={event.hasUnknownFlag ? 'person-event' : 'motion-event'}
              onClick={() => selectEvent(event)}
            >
              <img
                loading="lazy"
                src={`/api/v1/media/${segments.find((segment) => segment.startMs === event.startMs)?.id}/thumbnail`}
                alt=""
              />
              <span>
                <b>{event.hasUnknownFlag ? '有人移动' : '画面变动'}</b>
                {new Date(event.startMs).toLocaleTimeString([], {
                  hour: '2-digit',
                  minute: '2-digit',
                })}
              </span>
            </button>
          ))}
        </div>
      </aside>
    </main>
  );
}

type User = {
  id: string;
  username: string;
  role: 'admin' | 'user';
  active: boolean;
  createdAt: number;
};

type Folder = {
  id: string;
  name: string;
  serverPath?: string;
  mounted: boolean;
  scanStatus: 'pending' | 'scanning' | 'ready' | 'failed';
  lastScanAt?: number;
  firstDate?: string;
  message?: string;
};

async function api<T>(path: string, options: RequestInit = {}): Promise<T> {
  const response = await fetch(path, {
    ...options,
    headers: options.body
      ? { 'Content-Type': 'application/json', ...options.headers }
      : options.headers,
  });
  if (!response.ok) {
    const problem = await response.json().catch(() => ({}));
  if (response.status === 401 && problem.error === 'authentication_required') {
    location.assign('/login');
  }
    throw new Error(problem.error || 'request_failed');
  }
  return response.json() as Promise<T>;
}

function LoginPage() {
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const passwordChanged = new URLSearchParams(location.search).get('passwordChanged') === '1';

  return (
    <main className="auth-layout">
      <section className="auth-card">
        <p className="eyebrow">MIJIA ARCHIVE</p>
        <h1>登录</h1>
        <p>登录后才能访问录像、事件和管理功能。</p>
    {passwordChanged && <p className="form-success" role="status">密码已修改，请使用新密码登录。</p>}
        <form
          onSubmit={(event) => {
            event.preventDefault();
            setBusy(true);
            setError('');
            void api<{ user: User }>('/api/v1/auth/login', {
              method: 'POST',
              body: JSON.stringify({ username, password }),
            })
              .then(() => location.assign('/folders'))
              .catch((reason: Error) => {
                setError(reason.message === 'login_rate_limited' ? '尝试次数过多，请稍后再试。' : '用户名或密码错误。');
                setBusy(false);
              });
          }}
        >
          <label>
            用户名
            <input autoComplete="username" value={username} onChange={(event) => setUsername(event.target.value)} required />
          </label>
          <label>
            密码
            <input type="password" autoComplete="current-password" value={password} onChange={(event) => setPassword(event.target.value)} required />
          </label>
          {error && <p className="form-error" role="alert">{error}</p>}
          <button className="primary-button" type="submit" disabled={busy}>{busy ? '正在登录…' : '登录'}</button>
        </form>
      </section>
    </main>
  );
}

function AppHeader({ user, title = '米家录像回放系统' }: { user: User; title?: string }) {
  return (
    <header className="app-header">
      <div>
        <p className="eyebrow">MIJIA ARCHIVE</p>
        <h1>{title}</h1>
      </div>
      <nav aria-label="系统导航">
        <a href="/folders">文件夹</a>
        {user.role === 'admin' && <a href="/admin/users">用户</a>}
    <a href="/account/password">修改密码</a>
        <span>{user.username}</span>
        <button
          type="button"
          onClick={() => void api('/api/v1/auth/logout', { method: 'POST' }).then(() => location.assign('/login'))}
        >
          退出
        </button>
      </nav>
    </header>
  );
}

function FolderEditor({ folder, onSaved, onCancel }: { folder?: Folder; onSaved: () => void; onCancel?: () => void }) {
  const [name, setName] = useState(folder?.name || '');
  const [serverPath, setServerPath] = useState(folder?.serverPath || '');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  return (
    <form
      className="management-form"
      onSubmit={(event) => {
        event.preventDefault();
        setBusy(true);
        setError('');
        const path = folder ? `/api/v1/folders/${folder.id}` : '/api/v1/folders';
        void api(path, { method: folder ? 'PUT' : 'POST', body: JSON.stringify({ name, serverPath }) })
          .then(onSaved)
          .catch((reason: Error) => {
            setError(reason.message === 'folder_outside_library_or_invalid'
              ? '目录必须位于配置的录像库根目录内，并包含 MIJIA_RECORD_VIDEO、按小时分目录或平铺的米家录像。'
              : '保存失败，名称或路径可能已存在。');
            setBusy(false);
          });
      }}
    >
      <label>名称<input value={name} maxLength={80} onChange={(event) => setName(event.target.value)} required /></label>
      <label>服务器路径<input value={serverPath} maxLength={1024} onChange={(event) => setServerPath(event.target.value)} placeholder="/path/inside/media-library/archive" required /></label>
      <p className="field-help">只能使用部署配置允许的录像库根目录及其子目录；保存后会立即开始只读扫描。</p>
      {error && <p className="form-error" role="alert">{error}</p>}
      <div className="form-actions">
        <button className="primary-button" disabled={busy} type="submit">{busy ? '正在保存…' : '保存'}</button>
        {onCancel && <button type="button" onClick={onCancel}>取消</button>}
      </div>
    </form>
  );
}

function FoldersPage({ user, folders, reload }: { user: User; folders: Folder[]; reload: () => void }) {
  const [editing, setEditing] = useState<Folder>();
  const [adding, setAdding] = useState(false);
  return (
    <main className="management-layout">
      <AppHeader user={user} />
      <section className="folder-section">
        <div className="section-heading">
          <div><h2>录像文件夹</h2><p>选择一个文件夹进入录像回放。</p></div>
          {user.role === 'admin' && <button className="primary-button" onClick={() => setAdding(true)}>新增文件夹</button>}
        </div>
        <div className="folder-grid">
          {folders.map((folder) => (
            <article className="folder-card" key={folder.id}>
              <a
                className={!folder.mounted || !folder.firstDate ? 'disabled-folder' : ''}
                aria-disabled={!folder.mounted || !folder.firstDate}
                href={folder.mounted && folder.firstDate ? `/folders/${folder.id}/player?date=${folder.firstDate}` : undefined}
              >
                <span className="folder-icon" aria-hidden="true">▰</span>
                <strong>{folder.name}</strong>
                <small>{folder.mounted ? (folder.scanStatus === 'ready' ? (folder.firstDate ? '可播放' : '暂无录像') : (folder.firstDate ? '扫描中 · 可查看已有录像' : '正在索引')) : '目录不可用'}</small>
              </a>
              {folder.message && <p className="folder-message">{folder.message}</p>}
              {user.role === 'admin' && (
                <div className="folder-admin">
                  <code title={folder.serverPath}>{folder.serverPath}</code>
                  <button type="button" onClick={() => setEditing(folder)}>编辑</button>
                  <button type="button" onClick={() => void api(`/api/v1/folders/${folder.id}/scan`, { method: 'POST' }).then(reload)}>重新扫描</button>
                </div>
              )}
            </article>
          ))}
        </div>
      </section>
      {user.role === 'admin' && (adding || editing) && (
        <section className="editor-panel">
          <h2>{editing ? '编辑文件夹' : '新增文件夹'}</h2>
          <FolderEditor
            folder={editing}
            onCancel={() => { setAdding(false); setEditing(undefined); }}
            onSaved={() => { setAdding(false); setEditing(undefined); reload(); }}
          />
        </section>
      )}
    </main>
  );
}

function UsersPage({ user }: { user: User }) {
  const [users, setUsers] = useState<User[]>([]);
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [passwordConfirmation, setPasswordConfirmation] = useState('');
  const [role, setRole] = useState<'user' | 'admin'>('user');
  const [message, setMessage] = useState('');
  const load = () => void api<{ users: User[] }>('/api/v1/users').then((data) => setUsers(data.users));
  useEffect(load, []);
  return (
    <main className="management-layout">
      <AppHeader user={user} />
      <section className="users-panel">
        <div className="section-heading"><div><h2>系统用户</h2><p>只有管理员可以新增账号。</p></div></div>
        <div className="user-list">
          {users.map((item) => <div key={item.id}><strong>{item.username}</strong><span>{item.role === 'admin' ? '管理员' : '普通用户'}</span></div>)}
        </div>
        <h3>新增用户</h3>
        <form
          className="management-form compact-form"
          onSubmit={(event) => {
            event.preventDefault();
            setMessage('');
      if (password !== passwordConfirmation) {
        setMessage('两次输入的密码不一致。');
        return;
      }
            void api('/api/v1/users', { method: 'POST', body: JSON.stringify({ username, password, passwordConfirmation, role }) })
              .then(() => { setUsername(''); setPassword(''); setPasswordConfirmation(''); setMessage('用户已创建。'); load(); })
              .catch((reason: Error) => setMessage(reason.message === 'password_confirmation_mismatch'
        ? '两次输入的密码不一致。'
        : '创建失败：用户名需为 3–32 位，密码至少 8 位，且用户名不能重复。'));
          }}
        >
          <label>用户名<input autoComplete="off" value={username} onChange={(event) => setUsername(event.target.value)} required /></label>
      <label>密码<input type="password" autoComplete="new-password" value={password} minLength={8} onChange={(event) => setPassword(event.target.value)} required /></label>
      <label>确认密码<input type="password" autoComplete="new-password" value={passwordConfirmation} minLength={8} onChange={(event) => setPasswordConfirmation(event.target.value)} required /></label>
          <label>角色<select value={role} onChange={(event) => setRole(event.target.value as 'user' | 'admin')}><option value="user">普通用户</option><option value="admin">管理员</option></select></label>
          <button className="primary-button" type="submit">新增用户</button>
          {message && <p role="status">{message}</p>}
        </form>
      </section>
    </main>
  );
}

function ChangePasswordPage({ user }: { user: User }) {
  const [currentPassword, setCurrentPassword] = useState('');
  const [newPassword, setNewPassword] = useState('');
  const [confirmation, setConfirmation] = useState('');
  const [message, setMessage] = useState('');
  const [busy, setBusy] = useState(false);
  return (
    <main className="management-layout">
      <AppHeader user={user} title="账号设置" />
      <section className="users-panel">
        <h2>修改密码</h2>
        <p className="field-help">修改成功后，所有设备都需要使用新密码重新登录。</p>
        <form
          className="management-form compact-form password-form"
          onSubmit={(event) => {
            event.preventDefault();
            setMessage('');
            if (newPassword !== confirmation) {
              setMessage('两次输入的新密码不一致。');
              return;
            }
            setBusy(true);
            void api('/api/v1/auth/password', {
              method: 'POST',
              body: JSON.stringify({ currentPassword, newPassword, newPasswordConfirmation: confirmation }),
            })
              .then(() => location.assign('/login?passwordChanged=1'))
              .catch((reason: Error) => {
                setMessage(reason.message === 'invalid_current_password'
                  ? '旧密码错误。'
                  : reason.message === 'password_change_rate_limited'
                    ? '尝试次数过多，请稍后再试。'
                    : '修改失败，新密码必须为 8–128 个字符。');
                setBusy(false);
              });
          }}
        >
          <label>旧密码<input type="password" autoComplete="current-password" value={currentPassword} onChange={(event) => setCurrentPassword(event.target.value)} required /></label>
          <label>新密码<input type="password" autoComplete="new-password" minLength={8} value={newPassword} onChange={(event) => setNewPassword(event.target.value)} required /></label>
          <label>确认新密码<input type="password" autoComplete="new-password" minLength={8} value={confirmation} onChange={(event) => setConfirmation(event.target.value)} required /></label>
          <button className="primary-button" type="submit" disabled={busy}>{busy ? '正在修改…' : '修改密码'}</button>
          {message && <p className="form-error" role="alert">{message}</p>}
        </form>
      </section>
    </main>
  );
}

function AuthenticatedApp({ user }: { user: User }) {
  const [folders, setFolders] = useState<Folder[]>([]);
  const [loading, setLoading] = useState(true);
  const reload = useCallback(() => {
    void api<{ folders: Folder[] }>('/api/v1/folders').then((data) => { setFolders(data.folders); setLoading(false); });
  }, []);
  useEffect(() => { reload(); }, [reload]);
  useEffect(() => {
    if (!folders.some((folder) => folder.scanStatus === 'pending' || folder.scanStatus === 'scanning')) return;
    const timer = window.setInterval(reload, 3000);
    return () => window.clearInterval(timer);
  }, [folders, reload]);
  const playerMatch = location.pathname.match(/^\/folders\/([0-9a-f]{32})\/player$/);
  if (location.pathname === '/admin/users') {
    return user.role === 'admin' ? <UsersPage user={user} /> : <FoldersPage user={user} folders={folders} reload={reload} />;
  }
  if (location.pathname === '/account/password') {
    return <ChangePasswordPage user={user} />;
  }
  if (playerMatch) {
    const folder = folders.find((item) => item.id === playerMatch[1]);
    if (loading) return <main className="loading-page">正在加载…</main>;
    if (!folder?.mounted || !folder.firstDate) {
      location.replace('/folders');
      return null;
    }
    return <PlayerPage folderId={folder.id} folderName={folder.name} firstDate={folder.firstDate || new Date().toISOString().slice(0, 10)} />;
  }
  if (loading) return <main className="loading-page">正在加载…</main>;
  return <FoldersPage user={user} folders={folders} reload={reload} />;
}

function RootApp() {
  const [user, setUser] = useState<User>();
  const [loading, setLoading] = useState(location.pathname !== '/login');
  useEffect(() => {
    if (location.pathname === '/login') return;
    void api<{ user: User }>('/api/v1/auth/me').then((data) => { setUser(data.user); setLoading(false); });
  }, []);
  if (location.pathname === '/login') return <LoginPage />;
  if (loading || !user) return <main className="loading-page">正在验证登录状态…</main>;
  return <AuthenticatedApp user={user} />;
}

createRoot(document.getElementById('root')!).render(
  <React.StrictMode><RootApp /></React.StrictMode>,
);

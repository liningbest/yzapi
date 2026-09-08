import { useEffect, useState } from 'react';
import { dayjs } from '@/utils/format';

/** Returns "mm:ss" remaining until `until`, ticking each second; empty string when passed or absent. */
export function useCountdown(until: string | null | undefined): string {
  const [text, setText] = useState('');
  useEffect(() => {
    if (!until) {
      setText('');
      return;
    }
    const target = dayjs(until);
    if (!target.isValid()) {
      setText('');
      return;
    }
    const tick = () => {
      const diff = Math.max(0, target.diff(dayjs(), 'second'));
      if (diff <= 0) {
        setText('');
        return;
      }
      const m = Math.floor(diff / 60);
      const s = diff % 60;
      setText(`${String(m).padStart(2, '0')}:${String(s).padStart(2, '0')}`);
    };
    tick();
    const id = window.setInterval(tick, 1000);
    return () => window.clearInterval(id);
  }, [until]);
  return text;
}

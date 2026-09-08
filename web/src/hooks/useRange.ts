import { useMemo, useState } from 'react';
import type { Dayjs } from 'dayjs';
import type { RangeKey, RangeParams } from '@/types';

/** Range state (24h/7d/30d/custom) that produces API params. */
export function useRange(initial: RangeKey = '7d') {
  const [range, setRange] = useState<RangeKey>(initial);
  const [custom, setCustom] = useState<[Dayjs, Dayjs] | null>(null);

  const params = useMemo<RangeParams>(() => {
    if (range === 'custom' && custom) {
      return { range: 'custom', from: custom[0].startOf('day').toISOString(), to: custom[1].endOf('day').toISOString() };
    }
    if (range === 'custom') return { range: '7d' };
    return { range };
  }, [range, custom]);

  return { range, setRange, custom, setCustom, params };
}

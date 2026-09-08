import { useCallback, useMemo, useState } from 'react';
import type { TablePaginationConfig } from 'antd';
import { useTranslation } from 'react-i18next';
import { PAGE_SIZE } from '@/utils/constants';

/**
 * Keeps page / page_size / filter state for a list page and produces
 * antd Table pagination props. Changing filters resets to page 1.
 */
export function useTableQuery<F extends object>(initialFilters: F, initialPageSize = PAGE_SIZE) {
  const { t } = useTranslation();
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(initialPageSize);
  const [filters, setFiltersState] = useState<F>(initialFilters);

  const setFilters = useCallback((patch: Partial<F>) => {
    setFiltersState((prev) => ({ ...prev, ...patch }));
    setPage(1);
  }, []);

  const resetFilters = useCallback(() => {
    setFiltersState(initialFilters);
    setPage(1);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const params = useMemo(() => ({ ...filters, page, page_size: pageSize }), [filters, page, pageSize]);

  const pagination = useCallback(
    (total: number | undefined): TablePaginationConfig => ({
      current: page,
      pageSize,
      total: total ?? 0,
      showSizeChanger: true,
      showTotal: (tot) => t('common.totalItems', { total: tot }),
      pageSizeOptions: [10, 20, 50, 100],
      onChange: (p, ps) => {
        if (ps !== pageSize) {
          setPageSize(ps);
          setPage(1);
        } else {
          setPage(p);
        }
      },
    }),
    [page, pageSize, t],
  );

  return { page, pageSize, filters, setFilters, resetFilters, params, pagination, setPage };
}

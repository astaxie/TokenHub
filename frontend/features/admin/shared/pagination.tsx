import { ChevronLeft, ChevronRight, ChevronsLeft, ChevronsRight } from "lucide-react";
import { useEffect, useState } from "react";
import { activeLanguage, tx } from "../i18n/runtime";

export type PaginationState = {
  page: number;
  pageSize: number;
  pageCount: number;
  startIndex: number;
  endIndex: number;
  setPage: (page: number) => void;
  setPageSize: (pageSize: number) => void;
};

export const pageSizeOptions = [10, 20, 50, 100];

export function usePagination(totalItems: number, resetKey: string): PaginationState {
  const [page, setPageState] = useState(1);
  const [pageSize, setPageSizeState] = useState(20);
  const pageCount = Math.max(1, Math.ceil(totalItems / pageSize));

  useEffect(() => {
    setPageState(1);
  }, [resetKey]);

  useEffect(() => {
    if (page > pageCount) setPageState(pageCount);
  }, [page, pageCount]);

  const safePage = Math.min(page, pageCount);
  const startIndex = totalItems === 0 ? 0 : (safePage - 1) * pageSize;
  const endIndex = Math.min(startIndex + pageSize, totalItems);

  return {
    page: safePage,
    pageSize,
    pageCount,
    startIndex,
    endIndex,
    setPage: (nextPage) => setPageState(Math.min(Math.max(nextPage, 1), pageCount)),
    setPageSize: (nextPageSize) => {
      setPageSizeState(nextPageSize);
      setPageState(1);
    },
  };
}

export function PaginationControls({ pagination, totalItems }: { pagination: PaginationState; totalItems: number }) {
  if (totalItems <= pageSizeOptions[0]) return null;
  return (
    <div className="pagination">
      <div className="pagination-summary">
        {activeLanguage === "zh-CN"
          ? `第 ${pagination.startIndex + 1}-${pagination.endIndex} 条，共 ${totalItems} 条`
          : activeLanguage === "ja"
            ? `${pagination.startIndex + 1}-${pagination.endIndex} / ${totalItems} 件`
            : `${pagination.startIndex + 1}-${pagination.endIndex} of ${totalItems}`}
      </div>
      <div className="pagination-controls">
        <label className="page-size">
          <span>{tx("每页")}</span>
          <select value={pagination.pageSize} onChange={(event) => pagination.setPageSize(Number(event.target.value))}>
            {pageSizeOptions.map((option) => <option key={option} value={option}>{option}</option>)}
          </select>
        </label>
        <div className="page-buttons">
          <button type="button" title={tx("第一页")} onClick={() => pagination.setPage(1)} disabled={pagination.page <= 1}><ChevronsLeft size={15} /></button>
          <button type="button" title={tx("上一页")} onClick={() => pagination.setPage(pagination.page - 1)} disabled={pagination.page <= 1}><ChevronLeft size={15} /></button>
          <span>{pagination.page} / {pagination.pageCount}</span>
          <button type="button" title={tx("下一页")} onClick={() => pagination.setPage(pagination.page + 1)} disabled={pagination.page >= pagination.pageCount}><ChevronRight size={15} /></button>
          <button type="button" title={tx("最后一页")} onClick={() => pagination.setPage(pagination.pageCount)} disabled={pagination.page >= pagination.pageCount}><ChevronsRight size={15} /></button>
        </div>
      </div>
    </div>
  );
}

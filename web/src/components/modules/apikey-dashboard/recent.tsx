import { useTranslations } from 'use-intl';
import dayjs from 'dayjs';
import { History } from 'lucide-react';
import { cn, formatCount, formatMoney } from '@/lib/utils';
import type { APIKeyRecentRequest } from '@/api/apikey';

// formatMilliseconds 将毫秒转换为以秒或毫秒为单位的耗时文本。
function formatMilliseconds(value: number) {
    if (value >= 1000) return `${(value / 1000).toFixed(2)} s`;
    return `${Math.round(value)} ms`;
}

// 状态圆点配色: 成功绿色, 失败红色, 取消灰色, 进行中的请求常理上不会出现在已结束列表。
const statusDotClass: Record<APIKeyRecentRequest['status'], string> = {
    success: 'bg-chart-2',
    failed: 'bg-destructive',
    canceled: 'bg-muted-foreground/60',
    running: 'bg-chart-4 animate-pulse',
    committed: 'bg-chart-4 animate-pulse',
};

// RecentRequests 列出当前密钥最近已结束请求的用量明细, 记录仅存于进程内存。
export function RecentRequests({ requests }: { requests: APIKeyRecentRequest[] }) {
    const t = useTranslations('apiKeyDashboard');

    return (
        <div className="rounded-2xl border bg-card p-6">
            <div className="flex items-center gap-2 mb-4">
                <History className="w-5 h-5 text-chart-5" />
                <span className="font-semibold">{t('recent.title')}</span>
            </div>
            {requests.length === 0 ? (
                <p className="text-sm text-muted-foreground py-4 text-center">{t('recent.empty')}</p>
            ) : (
                <div className="overflow-x-auto -mx-2">
                    <table className="w-full min-w-[46rem] text-sm">
                        <thead>
                            <tr className="text-xs text-muted-foreground">
                                <th className="py-2 px-2 text-left font-medium">{t('recent.time')}</th>
                                <th className="py-2 px-2 text-left font-medium">{t('recent.model')}</th>
                                <th className="py-2 px-2 text-right font-medium">{t('recent.input')}</th>
                                <th className="py-2 px-2 text-right font-medium">{t('recent.output')}</th>
                                <th className="py-2 px-2 text-right font-medium">{t('recent.cacheRead')}</th>
                                <th className="py-2 px-2 text-right font-medium">{t('recent.cacheWrite')}</th>
                                <th className="py-2 px-2 text-right font-medium">{t('recent.firstToken')}</th>
                                <th className="py-2 px-2 text-right font-medium">{t('recent.tps')}</th>
                                <th className="py-2 px-2 text-right font-medium">{t('recent.cost')}</th>
                            </tr>
                        </thead>
                        <tbody>
                            {requests.map((request, index) => (
                                <tr key={`${request.time}-${index}`} className="border-t border-border/50">
                                    <td className="py-2.5 px-2 text-muted-foreground tabular-nums whitespace-nowrap">
                                        {dayjs(request.time).format('MM-DD HH:mm:ss')}
                                    </td>
                                    <td className="py-2.5 px-2 max-w-52">
                                        <div className="flex items-center gap-1.5 min-w-0">
                                            <span
                                                className={cn('size-2 shrink-0 rounded-full', statusDotClass[request.status])}
                                                title={t(`recent.status.${request.status}`)}
                                            />
                                            <span className="font-mono text-xs truncate" title={request.model}>{request.model}</span>
                                        </div>
                                    </td>
                                    <td className="py-2.5 px-2 text-right tabular-nums">
                                        {formatCount(request.input_token).formatted.value}
                                        <span className="text-xs text-muted-foreground ml-0.5">{formatCount(request.input_token).formatted.unit}</span>
                                    </td>
                                    <td className="py-2.5 px-2 text-right tabular-nums">
                                        {formatCount(request.output_token).formatted.value}
                                        <span className="text-xs text-muted-foreground ml-0.5">{formatCount(request.output_token).formatted.unit}</span>
                                    </td>
                                    <td className="py-2.5 px-2 text-right tabular-nums text-muted-foreground">
                                        {formatCount(request.cache_read).formatted.value}
                                        <span className="text-xs ml-0.5">{formatCount(request.cache_read).formatted.unit}</span>
                                    </td>
                                    <td className="py-2.5 px-2 text-right tabular-nums text-muted-foreground">
                                        {formatCount(request.cache_write).formatted.value}
                                        <span className="text-xs ml-0.5">{formatCount(request.cache_write).formatted.unit}</span>
                                    </td>
                                    <td className="py-2.5 px-2 text-right tabular-nums text-muted-foreground whitespace-nowrap">
                                        {request.first_token_ms > 0 ? formatMilliseconds(request.first_token_ms) : '-'}
                                    </td>
                                    <td className="py-2.5 px-2 text-right tabular-nums text-muted-foreground">
                                        {request.tps > 0 ? request.tps.toFixed(1) : '-'}
                                    </td>
                                    <td className="py-2.5 px-2 text-right tabular-nums">
                                        {formatMoney(request.cost).formatted.value}
                                        <span className="text-xs text-muted-foreground ml-0.5">{formatMoney(request.cost).formatted.unit}</span>
                                    </td>
                                </tr>
                            ))}
                        </tbody>
                    </table>
                </div>
            )}
        </div>
    );
}

import { useState } from 'react';
import { Cell, Pie, PieChart } from 'recharts';
import { useTranslations } from 'use-intl';
import { ChartPie } from 'lucide-react';
import { useTheme } from '@/provider/theme';
import { ChartContainer, ChartTooltip } from '@/components/ui/chart';
import type { UsageMetric } from './metric-toggle';
import { MetricToggle } from './metric-toggle';
import type { APIKeyStatsModelFormatted } from '@/api/apikey';

const CHART_COLOR_COUNT = 5;

// 饼图上的一个扇区。
interface PieEntry {
    model: string;
    value: number; // 所选指标的原始数值。
    fill: string;
    formatted: { value: string; unit: string }; // 所选指标的展示文本。
    totalToken: { value: string; unit: string };
    totalCost: { value: string; unit: string };
}

// PieTooltip 展示悬浮扇区的模型名、所选指标与占比。
function PieTooltip({ active, payload, total, metricLabel, shareLabel }: {
    active?: boolean;
    payload?: { payload: PieEntry }[];
    total: number;
    metricLabel: string;
    shareLabel: string;
}) {
    if (!active || !payload?.length) return null;
    const entry = payload[0].payload;

    return (
        <div className="grid min-w-[8rem] items-start gap-1.5 rounded-lg border border-border/50 bg-background px-2.5 py-1.5 text-xs shadow-xl">
            <div className="flex items-center gap-1.5">
                <span className="size-2.5 shrink-0 rounded-[2px]" style={{ backgroundColor: entry.fill }} />
                <span className="font-medium font-mono break-all">{entry.model}</span>
            </div>
            <div className="flex items-center justify-between gap-4">
                <span className="text-muted-foreground">{metricLabel}</span>
                <span className="font-mono font-medium text-foreground tabular-nums">{entry.formatted.value}{entry.formatted.unit}</span>
            </div>
            <div className="flex items-center justify-between gap-4">
                <span className="text-muted-foreground">{shareLabel}</span>
                <span className="font-mono font-medium text-foreground tabular-nums">{total > 0 ? `${((entry.value / total) * 100).toFixed(1)}%` : '0%'}</span>
            </div>
        </div>
    );
}

// ModelPie 以环形图展示当前密钥各模型的词元或费用占比, 右侧为带数值的图例。
export function ModelPie({ models }: { models: APIKeyStatsModelFormatted[] }) {
    const t = useTranslations('apiKeyDashboard');
    const [metric, setMetric] = useState<UsageMetric>('token');
    // 订阅主题: 切换时 :root 上 --chart-* 的取值会变, 需重渲染以重新取色。
    useTheme();

    // 取实际色值而非 var(): 与趋势图取色方案一致, 规避截图工具不解析填充色 var() 的问题。
    const colors = Array.from(
        { length: CHART_COLOR_COUNT },
        (_, index) => getComputedStyle(document.documentElement).getPropertyValue(`--chart-${index + 1}`).trim()
    );

    // 只画有消耗的模型, 按数值降序分配色板, 保证最大占比固定落在主色上。
    const entries = models
        .map((stat) => ({
            model: stat.model,
            value: metric === 'cost' ? stat.total_cost.raw : stat.total_token.raw,
            fill: '',
            formatted: metric === 'cost' ? stat.total_cost.formatted : stat.total_token.formatted,
            totalToken: stat.total_token.formatted,
            totalCost: stat.total_cost.formatted,
        }))
        .filter((entry) => entry.value > 0)
        .sort((a, b) => b.value - a.value)
        .map((entry, index) => ({ ...entry, fill: colors[index % CHART_COLOR_COUNT] }));

    // 从未有模型产生用量时不渲染整张卡片; 仅当前指标全为零时保留卡片并提示。
    if (models.length === 0) return null;

    const total = entries.reduce((sum, entry) => sum + entry.value, 0);
    const totalFormatted = metric === 'cost' ? entries[0].totalCost : entries[0].totalToken;
    const metricLabel = t(`metric.${metric}`);

    return (
        <div className="rounded-2xl border bg-card p-6">
            <div className="flex items-center gap-2 mb-4">
                <ChartPie className="w-5 h-5 text-chart-3" />
                <span className="font-semibold">{t('pie.title')}</span>
                <MetricToggle value={metric} onChange={setMetric} />
            </div>
            {entries.length === 0 ? (
                <p className="text-sm text-muted-foreground py-8 text-center">{t('heatmap.noData')}</p>
            ) : (
                <div className="flex flex-col md:flex-row items-center gap-6">
                    <div className="relative shrink-0">
                        <ChartContainer config={{}} className="h-48 w-48 aspect-auto">
                            <PieChart>
                                <ChartTooltip content={<PieTooltip total={total} metricLabel={metricLabel} shareLabel={t('pie.share')} />} />
                                <Pie data={entries} dataKey="value" nameKey="model" innerRadius="66%" outerRadius="94%" paddingAngle={2} strokeWidth={0}>
                                    {entries.map((entry) => (
                                        <Cell key={entry.model} fill={entry.fill} />
                                    ))}
                                </Pie>
                            </PieChart>
                        </ChartContainer>
                        <div className="absolute inset-0 flex flex-col items-center justify-center pointer-events-none">
                            <span className="text-xs text-muted-foreground">{metricLabel}</span>
                            <span className="text-sm font-semibold">
                                {totalFormatted.value}<span className="text-xs font-normal text-muted-foreground ml-0.5">{totalFormatted.unit}</span>
                            </span>
                        </div>
                    </div>
                    <div className="flex-1 w-full min-w-0 space-y-1.5">
                        {entries.map((entry) => (
                            <div key={entry.model} className="flex items-center gap-2 text-sm min-w-0">
                                <span className="size-2.5 shrink-0 rounded-[2px]" style={{ backgroundColor: entry.fill }} />
                                <span className="font-mono text-xs truncate flex-1 min-w-0" title={entry.model}>{entry.model}</span>
                                <span className="text-muted-foreground text-xs tabular-nums">{total > 0 ? `${((entry.value / total) * 100).toFixed(1)}%` : '0%'}</span>
                                <span className="font-medium text-xs tabular-nums w-20 text-right">
                                    {entry.formatted.value}<span className="font-normal text-muted-foreground ml-0.5">{entry.formatted.unit}</span>
                                </span>
                            </div>
                        ))}
                    </div>
                </div>
            )}
        </div>
    );
}

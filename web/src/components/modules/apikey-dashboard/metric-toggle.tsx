import { Fragment } from 'react';
import { useTranslations } from 'use-intl';
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs';

export type UsageMetric = 'token' | 'cost';

// MetricToggle 渲染词元/费用二选一的切换, 供热力图和模型饼图共用。
export function MetricToggle({ value, onChange }: { value: UsageMetric; onChange: (value: UsageMetric) => void }) {
    const t = useTranslations('apiKeyDashboard');

    return (
        <Tabs value={value} onValueChange={(next) => onChange(next as UsageMetric)} className="ml-auto">
            <TabsList variant="text" className="p-0">
                {/* 首尾只留内侧内边距, 使 / 分隔号与文字贴合。 */}
                {([['token', 'pr-0'], ['cost', 'pl-0']] as const).map(([key, padding], index) => (
                    <Fragment key={key}>
                        {index > 0 && (
                            <span aria-hidden="true" className="mx-1 inline-flex h-full -translate-y-px items-center text-sm font-medium leading-none text-muted-foreground/50">/</span>
                        )}
                        <TabsTrigger value={key} className={padding}>{t(`metric.${key}`)}</TabsTrigger>
                    </Fragment>
                ))}
            </TabsList>
        </Tabs>
    );
}

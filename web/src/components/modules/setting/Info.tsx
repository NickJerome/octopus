import { useTranslations } from 'use-intl';
import { Info, Tag, AlertTriangle, Download, Loader2 } from 'lucide-react';
import Github from '@thesvg/react/github';
import { useLatestInfo, useUpdateStatus, useUpdateCore, UpdateRestartTimeout } from '@/api/update';
import { Button } from '@/components/ui/button';
import { toast } from 'sonner';

const APP_VERSION = import.meta.env.VITE_APP_VERSION || 'unknown'; // 当前前端构建对应的应用版本。
const STABLE_VERSION = /^v\d+\.\d+\.\d+$/;

// SettingInfo 展示版本信息，并在更新后清理本项目的浏览器缓存。
export function SettingInfo() {
    const t = useTranslations('setting');
    const statusQuery = useUpdateStatus();
    const status = statusQuery.data;
    const latestInfoQuery = useLatestInfo(status?.enabled === true);
    const updateCore = useUpdateCore();

    const backendNowVersion = status?.current_version || '';
    const latestVersion = latestInfoQuery.data?.tag_name || '';

    // 前端版本与后端当前版本不一致 → 浏览器缓存问题
    const isCacheMismatch = STABLE_VERSION.test(backendNowVersion) && STABLE_VERSION.test(APP_VERSION) && backendNowVersion !== APP_VERSION;
    const hasNewVersion = status?.enabled && latestInfoQuery.data?.update_available;
    const updateNotice = statusQuery.isError || latestInfoQuery.isError ? t('info.checkFailed')
        : status?.disabled_reason === 'development' ? t('info.developmentBuild')
        : status?.disabled_reason === 'invalid_repository' ? t('info.invalidRepository')
        : status && !status.enabled ? t('info.updateDisabled')
        : latestInfoQuery.data?.no_release ? t('info.noRelease') : '';

    // clearCacheAndReload 清理当前应用目录的缓存和 Service Worker 注册后刷新页面。
    const clearCacheAndReload = async () => {
        const scope = new URL('./', document.baseURI); // 当前应用的 Service Worker 作用域及缓存所属目录。
        if ('caches' in window) {
            const names = await caches.keys();
            await Promise.all(names.filter((name) => name.startsWith(`octopus-${encodeURIComponent(scope.pathname)}-`)).map((name) => caches.delete(name)));
        }

        if ('serviceWorker' in navigator) {
            const registration = await navigator.serviceWorker.getRegistration(scope.href);
            if (registration && registration.scope === scope.href) await registration.unregister();
        }

        window.location.reload();
    };

    // handleForceRefresh 立即清理缓存并重新加载当前页面。
    const handleForceRefresh = () => {
        void clearCacheAndReload();
    };

    // handleUpdate 更新服务端程序，成功后清理旧前端缓存。
    const handleUpdate = () => {
        updateCore.mutate(latestVersion, {
            onSuccess: () => {
                toast.success(t('info.updateSuccess'));
                void clearCacheAndReload();
            },
            onError: (error) => {
                toast.error(error instanceof UpdateRestartTimeout ? t('info.restartTimeout') : `${t('info.updateFailed')}: ${error.message}`);
            }
        });
    };

    return (
        <div className="rounded-3xl border border-border bg-card p-6 space-y-5">
            <h2 className="text-lg font-bold text-card-foreground flex items-center gap-2">
                <Info className="h-5 w-5" />
                {t('info.title')}
            </h2>
            {/* GitHub 仓库 */}
            <div className="flex items-center justify-between gap-4">
                <div className="flex items-center gap-3">
                    <Github variant="mono" className="h-5 w-5 text-muted-foreground" />
                    <span className="text-sm font-medium">{t('info.updateSource')}</span>
                </div>
                {status?.repository_url ? <a
                    href={status.repository_url}
                    target="_blank"
                    rel="noopener noreferrer"
                    className="text-sm text-primary hover:underline"
                >
                    {status.repository}
                </a> : <span className="text-sm text-muted-foreground">{status?.repository || t('info.unknown')}</span>}
            </div>
            {/* 当前版本 */}
            <div className="flex items-center justify-between gap-4">
                <div className="flex items-center gap-3">
                    <Tag className="h-5 w-5 text-muted-foreground" />
                    <span className="text-sm font-medium">{t('info.currentVersion')}</span>
                </div>
                <div className="flex items-center gap-2">
                    {statusQuery.isLoading ? (
                        <Loader2 className="size-4 animate-spin text-muted-foreground" />
                    ) : (
                        <code className="text-sm font-mono text-muted-foreground">
                            {backendNowVersion || t('info.unknown')}
                        </code>
                    )}
                </div>
            </div>

            {/* 最新版本 */}
            <div className="flex items-center justify-between gap-4">
                <div className="flex items-center gap-3">
                    <Download className="h-5 w-5 text-muted-foreground" />
                    <span className="text-sm font-medium">{t('info.latestVersion')}</span>
                </div>
                <div className="flex items-center gap-2">
                    {latestInfoQuery.isLoading ? (
                        <Loader2 className="size-4 animate-spin text-muted-foreground" />
                    ) : (
                        <code className="text-sm font-mono text-muted-foreground">
                            {status?.enabled ? latestVersion || t('info.unknown') : '—'}
                        </code>
                    )}
                </div>
            </div>

            {updateNotice && <p className="text-sm text-muted-foreground">{updateNotice}</p>}

            {/* 浏览器缓存问题警告 */}
            {isCacheMismatch && (
                <div className="p-3 bg-destructive/10 border border-destructive/20 rounded-xl space-y-2">
                    <div className="flex items-start gap-3">
                        <AlertTriangle className="h-5 w-5 text-destructive shrink-0 mt-0.5" />
                        <div className="flex-1 space-y-1">
                            <p className="text-sm text-destructive font-medium">
                                {t('info.versionMismatch')}
                            </p>
                            <p className="text-xs text-muted-foreground">
                                {t('info.versionMismatchHint', { frontend: APP_VERSION, backend: backendNowVersion })}
                            </p>
                        </div>
                    </div>
                    <div className="flex justify-end">
                        <Button
                            variant="destructive"
                            size="sm"
                            onClick={handleForceRefresh}
                            className="rounded-xl"
                        >
                            {t('info.forceRefresh')}
                        </Button>
                    </div>
                </div>
            )}

            {/* 有新版本可更新 */}
            {hasNewVersion && (
                <div className="p-3 bg-primary/10 border border-primary/20 rounded-xl space-y-2">
                    <div className="flex items-start gap-3">
                        <Download className="h-5 w-5 text-primary shrink-0 mt-0.5" />
                        <div className="flex-1 space-y-1">
                            <p className="text-sm text-primary font-medium">
                                {t('info.newVersionAvailable')}
                            </p>
                            <p className="text-xs text-muted-foreground">
                                {t('info.newVersionAvailableHint')}
                            </p>
                            <a className="text-xs text-primary hover:underline" href={latestInfoQuery.data?.release_url} target="_blank" rel="noopener noreferrer">
                                {t('info.releaseNotes')}
                            </a>
                        </div>
                    </div>
                    <div className="flex justify-end">
                        <Button
                            variant="default"
                            size="sm"
                            onClick={handleUpdate}
                            disabled={updateCore.isPending}
                            className="rounded-xl"
                        >
                            {updateCore.isPending ? t('info.updating') : t('info.updateNow')}
                        </Button>
                    </div>
                </div>
            )}
        </div>
    );
}

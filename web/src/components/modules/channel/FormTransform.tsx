import { useState } from 'react';
import { useMutation } from '@tanstack/react-query';
import { ArrowDown, ArrowUp, Code, List, Trash2 } from 'lucide-react';
import { useTranslations } from 'use-intl';
import { parseTransform, previewTransform, type TransformConfig, type TransformProtocol, type TransformRule } from '@/api/request-transform';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Switch } from '@/components/ui/switch';
import { IconButton } from '@/components/common/IconButton';
import type { ChannelFormState } from './state';

const protocols: { value: TransformProtocol; label: string }[] = [
    { value: 'openai_chat', label: 'OpenAI Chat' },
    { value: 'openai_responses', label: 'OpenAI Responses' },
    { value: 'anthropic_messages', label: 'Anthropic' },
];
const textClass = 'min-h-32 w-full rounded-xl border border-border bg-background px-3 py-2 text-xs font-mono focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring';
const selectClass = 'h-9 min-w-0 rounded-xl border border-border bg-background px-2 text-sm';

function sample(protocol: TransformProtocol) {
    const text = 'You are a helpful assistant.\n<total_tokens>120000</total_tokens>\nRemove this entire suffix too.';
    if (protocol === 'openai_responses') return JSON.stringify({ model: 'sample-model', instructions: text, input: 'Hello', reasoning: { effort: 'low', summary: 'auto' } }, null, 2);
    if (protocol === 'anthropic_messages') return JSON.stringify({ model: 'sample-model', max_tokens: 16384, system: [{ type: 'text', text, cache_control: { type: 'ephemeral' } }], messages: [{ role: 'user', content: 'Hello' }] }, null, 2);
    return JSON.stringify({ model: 'sample-model', messages: [{ role: 'system', content: text }, { role: 'user', content: 'Hello' }], reasoning_effort: 'low' }, null, 2);
}

export function FormTransform({ state, setState, idPrefix }: {
    state: ChannelFormState;
    setState: (state: ChannelFormState) => void;
    idPrefix: string;
}) {
    const t = useTranslations('channel.transform');
    const [jsonMode, setJSONMode] = useState(false);
    const [effort, setEffort] = useState('high');
    const [onlyMissing, setOnlyMissing] = useState(false);
    const [presetModel, setPresetModel] = useState('');
    const [stage, setStage] = useState<'client' | 'upstream'>('client');
    const [source, setSource] = useState<TransformProtocol>('openai_responses');
    const [target, setTarget] = useState<TransformProtocol>('openai_responses');
    const [model, setModel] = useState(state.models[0] || 'sample-model');
    const [body, setBody] = useState(sample('openai_responses'));
    const [submitted, setSubmitted] = useState('');
    const preview = useMutation({ mutationFn: previewTransform });
    let config: TransformConfig | null = null;
    let error = '';
    try { config = parseTransform(state.request_transform); } catch (err) { error = err instanceof Error ? err.message : String(err); }
    const rules = config?.rules ?? [];
    const snapshot = JSON.stringify([state.request_transform, state.param_override, stage, source, target, model, body]);
    const currentPreview = submitted === snapshot;

    const update = (rules: TransformRule[]) => setState({ ...state, request_transform: JSON.stringify({ version: 1, rules }, null, 2) });
    const add = (added: Omit<TransformRule, 'id'>[]) => update([
        ...rules, ...added.map((rule) => ({ ...rule, id: globalThis.crypto?.randomUUID?.() ?? `rule-${Date.now()}-${Math.random().toString(36).slice(2)}` })),
    ]);
    const move = (index: number, offset: number) => {
        const next = [...rules];
        [next[index], next[index + offset]] = [next[index + offset], next[index]];
        update(next);
    };
    const addEffort = () => add(protocols.slice(0, 2).map(({ value }) => ({
        name: `${t('effort')} · ${effort}`,
        when: { target_protocol: [value], ...(presetModel.trim() ? { target_model: [presetModel.trim()] } : {}) },
        actions: [{ op: 'set', target: { path: value === 'openai_chat' ? '/reasoning_effort' : '/reasoning/effort' }, value: effort, create_missing: true, only_if_missing: onlyMissing }],
    })));
    const runPreview = () => {
        setSubmitted(snapshot);
        preview.mutate({
            stage, body, param_override: state.param_override, request_transform: config,
            context: { source_protocol: source, target_protocol: target, source_model: stage === 'upstream' ? model : '', target_model: model, stream: false },
        });
    };

    return (
        <section className="space-y-3 border-t border-border pt-4">
            <div className="flex items-center justify-between gap-2">
                <Label>{t('title')}</Label>
                <Button type="button" variant="ghost" size="sm" onClick={() => setJSONMode(!jsonMode)}>
                    {jsonMode ? <List className="size-4" /> : <Code className="size-4" />}
                    {jsonMode ? t('rules') : t('json')}
                </Button>
            </div>
            <p className="text-xs text-muted-foreground">{t('description')}</p>
            <div className="rounded-xl border border-border p-3 space-y-3">
                <Button type="button" variant="outline" size="sm" disabled={!!error} onClick={() => add([{
                    name: t('cleanTokens'),
                    actions: [{ op: 'regex_replace', target: { scope: 'prompt_text', roles: ['system', 'developer'] }, pattern: '<total_tokens>[\\s\\S]*', replacement: '', replacement_mode: 'literal' }],
                }])}>{t('cleanTokens')}</Button>
                <p className="text-xs text-muted-foreground">{t('cleanHint', { marker: '<total_tokens>' })}</p>
                <div className="flex flex-wrap items-center gap-2">
                    <select aria-label={t('effort')} value={effort} onChange={(e) => setEffort(e.target.value)} className={selectClass}>
                        {['none', 'minimal', 'low', 'medium', 'high', 'xhigh'].map((value) => <option key={value}>{value}</option>)}
                    </select>
                    <Button type="button" variant="outline" size="sm" disabled={!!error} onClick={addEffort}>{t('addEffort')}</Button>
                    <label className="flex items-center gap-2 text-xs"><Switch checked={onlyMissing} onCheckedChange={setOnlyMissing} />{t('onlyMissing')}</label>
                </div>
                <Input value={presetModel} onChange={(e) => setPresetModel(e.target.value)} aria-label={t('modelFilter')} placeholder={t('modelFilter')} className="rounded-xl text-xs" />
                <p className="text-xs text-muted-foreground">{t('effortHint')}</p>
                <div className="flex flex-wrap gap-2">
                    <Button type="button" variant="outline" size="sm" disabled={!!error} onClick={() => { add([{ name: t('removeField'), actions: [{ op: 'remove', target: { path: '/metadata' } }] }]); setJSONMode(true); }}>{t('removeField')}</Button>
                    <Button type="button" variant="outline" size="sm" disabled={!!error} onClick={() => { add([{ name: t('replaceText'), actions: [{ op: 'replace', target: { scope: 'prompt_text', roles: ['system', 'developer'] }, pattern: 'old text', replacement: 'new text' }] }]); setJSONMode(true); }}>{t('replaceText')}</Button>
                </div>
            </div>
            {(jsonMode || !!error) ? (
                <div className="space-y-2">
                    <textarea id={`${idPrefix}-transform-json`} aria-label={t('json')} aria-invalid={!!error} spellCheck={false} value={state.request_transform} onChange={(e) => setState({ ...state, request_transform: e.target.value })} className={`${textClass} min-h-64`} placeholder={'{ "version": 1, "rules": [] }'} />
                    {error && <p role="alert" className="text-xs text-destructive break-all">{error}</p>}
                    <p className="text-xs text-muted-foreground">{t('jsonHint')}</p>
                </div>
            ) : rules.length === 0 ? <p className="text-xs text-muted-foreground">{t('empty')}</p> : (
                <div className="space-y-2">
                    {rules.map((rule, index) => (
                        <div key={`${rule.id}-${index}`} className="rounded-xl border border-border p-3 space-y-2">
                            <div className="flex items-center gap-2">
                                <Switch aria-label={t('enabled')} checked={rule.enabled !== false} onCheckedChange={(enabled) => update(rules.map((r, i) => i === index ? { ...r, enabled } : r))} />
                                <Input aria-label={t('ruleName')} value={rule.name ?? rule.id} onChange={(e) => update(rules.map((r, i) => i === index ? { ...r, name: e.target.value } : r))} className="min-w-0 rounded-xl text-xs" />
                                <IconButton className="size-7 shrink-0" aria-label={t('moveUp')} tip={t('moveUp')} disabled={index === 0} onClick={() => move(index, -1)}><ArrowUp className="size-4" /></IconButton>
                                <IconButton className="size-7 shrink-0" aria-label={t('moveDown')} tip={t('moveDown')} disabled={index === rules.length - 1} onClick={() => move(index, 1)}><ArrowDown className="size-4" /></IconButton>
                                <IconButton className="size-7 shrink-0" aria-label={t('removeRule')} tip={t('removeRule')} onClick={() => update(rules.filter((_, i) => i !== index))}><Trash2 className="size-4" /></IconButton>
                            </div>
                            <p className="text-xs text-muted-foreground break-all">{rule.when ? JSON.stringify(rule.when) : t('allRequests')}</p>
                            {rule.actions.map((action, i) => <p key={i} className="font-mono text-xs break-all">{action.op} · {action.target.path ?? t('promptText')}{action.value !== undefined ? ` = ${JSON.stringify(action.value)}` : action.pattern ? ` · ${action.pattern}` : ''}</p>)}
                        </div>
                    ))}
                </div>
            )}
            <details className="rounded-xl border border-border p-3">
                <summary className="cursor-pointer text-sm font-medium">{t('preview')}</summary>
                <div className="space-y-3 pt-3">
                    <p className="text-xs text-muted-foreground">{t('previewHint')}</p>
                    <div className="flex flex-wrap gap-2">
                        <select aria-label={t('inputStage')} value={stage} onChange={(e) => setStage(e.target.value as typeof stage)} className={selectClass}>
                            <option value="client">{t('clientBody')}</option><option value="upstream">{t('upstreamBody')}</option>
                        </select>
                        <select aria-label={t('sourceProtocol')} value={source} onChange={(e) => setSource(e.target.value as TransformProtocol)} className={selectClass}>
                            {protocols.map((p) => <option key={p.value} value={p.value}>{t('source')} · {p.label}</option>)}
                        </select>
                        <select aria-label={t('targetProtocol')} value={target} onChange={(e) => setTarget(e.target.value as TransformProtocol)} className={selectClass}>
                            {protocols.map((p) => <option key={p.value} value={p.value}>{t('target')} · {p.label}</option>)}
                        </select>
                    </div>
                    <Input aria-label={stage === 'client' ? t('targetModel') : t('sourceModel')} value={model} onChange={(e) => setModel(e.target.value)} placeholder={stage === 'client' ? t('targetModel') : t('sourceModel')} className="rounded-xl" />
                    <Button type="button" variant="ghost" size="sm" onClick={() => setBody(sample(stage === 'client' ? source : target))}>{t('loadSample')}</Button>
                    <textarea aria-label={t('sampleBody')} spellCheck={false} value={body} onChange={(e) => setBody(e.target.value)} className={textClass} />
                    <Button type="button" variant="outline" size="sm" disabled={!!error || preview.isPending || !body.trim()} onClick={runPreview}>{preview.isPending ? t('previewing') : t('runPreview')}</Button>
                    {currentPreview && preview.error && <p role="alert" className="text-xs text-destructive break-all">{preview.error.message}</p>}
                    {currentPreview && preview.data && <div className="space-y-3">
                        {preview.data.trace.map((entry, i) => <div key={i} className="rounded-lg bg-muted/50 p-2 text-xs space-y-1">
                            <p className="break-all font-medium">{rules.find((rule) => rule.id === entry.rule_id)?.name || entry.rule_id} · {entry.action || '—'} · {t(entry.status)}</p>
                            <p>{t('counts', { matched: entry.matched, replacements: entry.replacements })}</p>
                            {entry.paths.map((path) => <p key={path} className="font-mono break-all">{path}</p>)}
                            {entry.warnings.map((warning) => <p key={warning} className="text-amber-600 break-all">{warning}</p>)}
                        </div>)}
                        <div className="grid grid-cols-1 xl:grid-cols-2 gap-3">
                            {(['before', 'after'] as const).map((key) => <div key={key} className="min-w-0 space-y-1"><Label>{t(key)}</Label><pre className="max-h-80 overflow-auto rounded-lg bg-muted p-3 text-xs whitespace-pre-wrap break-all">{preview.data[key]}</pre></div>)}
                        </div>
                    </div>}
                </div>
            </details>
        </section>
    );
}

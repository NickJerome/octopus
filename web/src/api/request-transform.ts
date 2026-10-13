import { apiRequest } from './client';

export type TransformProtocol = 'openai_chat' | 'openai_responses' | 'anthropic_messages';
export type TransformAction = {
    op: 'set' | 'remove' | 'merge' | 'replace' | 'regex_replace';
    target: { path?: string; scope?: 'prompt_text'; roles?: ('system' | 'developer')[] };
    value?: unknown;
    create_missing?: boolean;
    only_if_missing?: boolean;
    require_match?: boolean;
    pattern?: string;
    replacement?: string;
    replacement_mode?: 'literal' | 'expand';
    limit?: number;
};
export type TransformRule = {
    id: string;
    name?: string;
    enabled?: boolean;
    when?: {
        source_protocol?: TransformProtocol[];
        target_protocol?: TransformProtocol[];
        source_model?: string[];
        target_model?: string[];
        stream?: boolean;
        exists?: string[];
        equals?: Record<string, unknown>;
    };
    actions: TransformAction[];
};
export type TransformConfig = { version: 1; rules: TransformRule[] };
export type TransformContext = {
    source_protocol: TransformProtocol;
    target_protocol: TransformProtocol;
    source_model: string;
    target_model: string;
    stream: boolean;
};
export type TransformPreviewInput = {
    stage: 'client' | 'upstream';
    body: string;
    context: TransformContext;
    param_override: string;
    request_transform: TransformConfig | null;
};
export type TransformPreview = {
    before: string;
    after: string;
    context: TransformContext;
    trace: {
        rule_id: string;
        action: number;
        op?: string;
        status: 'skipped' | 'unchanged' | 'modified' | 'failed';
        matched: number;
        replacements: number;
        paths: string[];
        warnings: string[];
    }[];
};

// Structural checks keep the card editor usable; operation and regex validation is authoritative on the server.
export function parseTransform(source: string): TransformConfig | null {
    if (!source.trim()) return null;
    const config = JSON.parse(source);
    if (config === null) return null;
    if (!config || config.version !== 1 || !Array.isArray(config.rules)
        || !config.rules.every((rule: TransformRule) => rule && typeof rule.id === 'string'
            && Array.isArray(rule.actions) && rule.actions.every((action) => action && typeof action.op === 'string' && action.target))) {
        throw new Error('Expected { "version": 1, "rules": [{ "id": "…", "actions": […] }] }');
    }
    return config as TransformConfig;
}

export function previewTransform(input: TransformPreviewInput) {
    return apiRequest<TransformPreview>('/api/v1/channel/preview-transform', { method: 'POST', body: input });
}

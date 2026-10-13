import { useMutation, useQuery } from '@tanstack/react-query';
import { apiRequest, ApiError } from './client';

interface UpdateStatus {
    repository: string;
    repository_url: string;
    current_version: string;
    enabled: boolean;
    disabled_reason?: 'disabled' | 'development' | 'invalid_repository';
}

interface LatestInfo {
    tag_name: string;
    published_at: string;
    body: string;
    release_url: string;
    update_available: boolean;
    no_release: boolean;
}

export function useUpdateStatus() {
    return useQuery({
        queryKey: ['update', 'status'],
        queryFn: () => apiRequest<UpdateStatus>('/api/v1/update/status'),
        refetchOnMount: 'always',
    });
}

export function useLatestInfo(enabled = true) {
    return useQuery({
        queryKey: ['update', 'latest'],
        queryFn: () => apiRequest<LatestInfo>('/api/v1/update'),
        enabled,
        refetchInterval: 3600000,
        refetchOnMount: 'always',
    });
}

export class UpdateRestartTimeout extends Error {}

// Keep the mutation pending until the expected backend is actually serving requests.
async function waitForVersion(version: string) {
    const deadline = Date.now() + 120_000;
    while (Date.now() < deadline) {
        await new Promise((resolve) => setTimeout(resolve, 1500));
        try {
            const current = await apiRequest<string>(`/api/v1/update/now-version?t=${Date.now()}`, {
                signal: AbortSignal.timeout(5000),
                dispatchUnauthorized: false,
            });
            if (current === version) return;
        } catch {
            // Brief disconnections are expected while the process restarts.
        }
    }
    throw new UpdateRestartTimeout('The expected version did not become available within two minutes');
}

export function useUpdateCore() {
    return useMutation({
        mutationFn: async (version: string) => {
            try {
                await apiRequest<{ version: string }>('/api/v1/update', {
                    method: 'POST', body: { version },
                });
            } catch (error) {
                if (error instanceof ApiError && ![502, 503, 504].includes(error.status)) throw error;
                // If restart interrupted the response, check the version before reporting failure.
            }
            await waitForVersion(version);
        },
    });
}

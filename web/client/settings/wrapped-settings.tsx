import { useCallback, useEffect, useState } from 'react';

import { SettingsSection } from '/@/renderer/features/settings/components/settings-section';
import { Button } from '/@/shared/components/button/button';
import { Switch } from '/@/shared/components/switch/switch';
import { Text } from '/@/shared/components/text/text';

/**
 * samo Wrapped: the listener's year, at /wrapped. It reads from their Last.fm
 * account by default. The switch here is the opt-in for samo keeping its own
 * history of what they play (and counting their audiobooks and podcasts), so
 * a Wrapped no longer needs Last.fm. Off unless they turn it on; each person
 * decides for themselves (internal/wrapped).
 */

interface WrappedSettingsState {
    enabledAt?: string;
    listens: number;
    localHistory: boolean;
}

const api = async (path: string, init: RequestInit = {}) => {
    const response = await fetch(path, {
        ...init,
        headers: {
            Authorization: `Bearer ${localStorage.getItem('samo-token') ?? ''}`,
            'Content-Type': 'application/json',
        },
    });
    const body = await response.json().catch(() => ({}));
    if (!response.ok) throw new Error(body.error || `Request failed (${response.status})`);
    return body;
};

const since = (iso?: string) =>
    iso ? new Date(iso).toLocaleDateString(undefined, { day: 'numeric', month: 'long', year: 'numeric' }) : '';

export const WrappedSettings = () => {
    const [state, setState] = useState<null | WrappedSettingsState>(null);
    const [busy, setBusy] = useState(false);
    const [error, setError] = useState('');
    const year = new Date().getFullYear();

    const refresh = useCallback(async () => {
        try {
            setState(await api('/api/v1/wrapped/settings'));
        } catch (e) {
            setError((e as Error).message);
        }
    }, []);

    useEffect(() => {
        refresh();
    }, [refresh]);

    const toggle = async (on: boolean) => {
        setBusy(true);
        setError('');
        try {
            setState(await api('/api/v1/wrapped/settings', { body: JSON.stringify({ localHistory: on }), method: 'PUT' }));
        } catch (e) {
            setError((e as Error).message);
        } finally {
            setBusy(false);
        }
    };

    const erase = async () => {
        if (!window.confirm('Delete the listening history samo has kept for you? Your Last.fm is not affected.')) return;
        setBusy(true);
        try {
            setState(await api('/api/v1/wrapped/history', { method: 'DELETE' }));
        } catch (e) {
            setError((e as Error).message);
        } finally {
            setBusy(false);
        }
    };

    const history = state?.localHistory
        ? `On${state.enabledAt ? ` since ${since(state.enabledAt)}` : ''}. samo logs the music you play, and counts your audiobooks and podcasts, for your Wrapped. ${state.listens.toLocaleString()} ${state.listens === 1 ? 'listen' : 'listens'} so far.`
        : "Off. Your Wrapped comes from your Last.fm account. Turn this on and samo keeps its own history of what you play instead, so you don't need Last.fm, and your audiobooks and podcasts count too. Only you can see it.";

    return (
        <SettingsSection
            extra={
                <>
                    {state && state.listens > 0 && (
                        <div>
                            <Button disabled={busy} onClick={erase} size="compact-md" variant="subtle">
                                Delete samo's history of my listening
                            </Button>
                        </div>
                    )}
                    {error && (
                        <Text size="sm" style={{ color: 'var(--theme-colors-state-error)' }}>
                            {error}
                        </Text>
                    )}
                </>
            }
            options={[
                {
                    control: (
                        <Button component="a" href="/wrapped" size="compact-md" variant="filled">
                            Open
                        </Button>
                    ),
                    description: `Your ${year} in music: top artists, songs and albums, how much you listened, and when.`,
                    title: `Your ${year} Wrapped`,
                },
                {
                    control: (
                        <Switch
                            aria-label="Keep my listening history on samo"
                            checked={Boolean(state?.localHistory)}
                            disabled={!state || busy}
                            onChange={(event) => toggle(event.currentTarget.checked)}
                        />
                    ),
                    description: history,
                    title: 'Keep my listening history on samo',
                },
            ]}
            title="samo Wrapped"
        />
    );
};

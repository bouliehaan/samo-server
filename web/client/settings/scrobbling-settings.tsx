import { useCallback, useEffect, useState } from 'react';

import { SettingsSection } from '/@/renderer/features/settings/components/settings-section';
import { Button } from '/@/shared/components/button/button';
import { Group } from '/@/shared/components/group/group';
import { Text } from '/@/shared/components/text/text';

/**
 * Each samo user links their own Last.fm account, and only the music played
 * while signed in as them is scrobbled to it (docs/lastfm.md). The admin
 * dashboard had the only screen for linking one, so anyone who is not an
 * admin had no way to. This is that screen in the player's own settings.
 *
 * Last.fm's sign-in does not call back: samo gets a token, the listener
 * approves it on last.fm, and samo then trades it for a session. The trade is
 * tried by itself whenever the page is looked at again (coming back from the
 * Last.fm tab or app) and every few seconds while it is open, so there is no
 * second step to remember. The token is kept across a reload, which a phone
 * may do to a page left in the background.
 */

interface LastFMStatus {
    connected: boolean;
    enabled: boolean;
    queueSize: number;
    username?: string;
}

interface Pending {
    at: number;
    authUrl: string;
    token: string;
}

const PENDING_KEY = 'samo-lastfm-pending';
// Last.fm tokens are good for an hour.
const PENDING_LIFETIME = 60 * 60 * 1000;

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

const readPending = (): null | Pending => {
    try {
        const pending = JSON.parse(localStorage.getItem(PENDING_KEY) || 'null') as null | Pending;
        return pending && Date.now() - pending.at < PENDING_LIFETIME ? pending : null;
    } catch {
        return null;
    }
};

const clearPending = () => {
    try {
        localStorage.removeItem(PENDING_KEY);
    } catch {
        // storage unavailable: nothing was kept
    }
};

export const ScrobblingSettings = () => {
    const [status, setStatus] = useState<LastFMStatus | null>(null);
    const [pending, setPending] = useState<null | Pending>(readPending);
    const [busy, setBusy] = useState(false);
    const [error, setError] = useState('');
    const account = ((window as any).SAMO_HOST_USER?.name as string | undefined) ?? 'you';

    const refresh = useCallback(async () => {
        try {
            setStatus(await api('/api/v1/lastfm/status'));
        } catch {
            setStatus({ connected: false, enabled: false, queueSize: 0 });
        }
    }, []);

    useEffect(() => {
        refresh();
    }, [refresh]);

    const complete = useCallback(
        async (quiet: boolean) => {
            const current = readPending();
            if (!current) return;
            try {
                await api('/api/v1/lastfm/auth/complete', {
                    body: JSON.stringify({ token: current.token }),
                    method: 'POST',
                });
                clearPending();
                setPending(null);
                setError('');
                await refresh();
            } catch {
                if (!quiet) {
                    setError('Last.fm has not been approved yet. Approve samo there, then try again.');
                }
            }
        },
        [refresh],
    );

    useEffect(() => {
        if (!pending) return undefined;
        const attempt = () => {
            if (document.visibilityState === 'visible') complete(true);
        };
        document.addEventListener('visibilitychange', attempt);
        window.addEventListener('focus', attempt);
        const timer = window.setInterval(attempt, 4000);
        return () => {
            document.removeEventListener('visibilitychange', attempt);
            window.removeEventListener('focus', attempt);
            window.clearInterval(timer);
        };
    }, [complete, pending]);

    const begin = async () => {
        setBusy(true);
        setError('');
        try {
            const response = await api('/api/v1/lastfm/auth/begin', { method: 'POST' });
            const next = { at: Date.now(), authUrl: response.authUrl, token: response.token };
            try {
                localStorage.setItem(PENDING_KEY, JSON.stringify(next));
            } catch {
                // kept in memory only: it still completes while the page stays open
            }
            setPending(next);
        } catch (e) {
            setError((e as Error).message);
        } finally {
            setBusy(false);
        }
    };

    const cancel = () => {
        clearPending();
        setPending(null);
        setError('');
    };

    const disconnect = async () => {
        if (!window.confirm('Stop scrobbling to Last.fm?')) return;
        setBusy(true);
        try {
            await api('/api/v1/lastfm/auth/session', { method: 'DELETE' });
            await refresh();
        } catch (e) {
            setError((e as Error).message);
        } finally {
            setBusy(false);
        }
    };

    let description: string;
    let control: React.ReactNode = null;
    if (!status) {
        description = 'Checking…';
    } else if (!status.enabled) {
        description =
            "Last.fm isn't set up on this server yet. Whoever runs samo can turn it on in Server admin → Settings → Account.";
    } else if (status.connected) {
        const waiting =
            status.queueSize > 0
                ? ` ${status.queueSize} ${status.queueSize === 1 ? 'listen is' : 'listens are'} waiting to be sent.`
                : '';
        description = `Music played while signed in as ${account} is scrobbled to ${status.username ?? 'your Last.fm account'}.${waiting}`;
        control = (
            <Button disabled={busy} onClick={disconnect} size="compact-md" variant="default">
                Disconnect
            </Button>
        );
    } else if (pending) {
        description =
            'Open Last.fm and approve samo, then come back here. It finishes by itself once you have.';
        control = (
            <Group gap="xs">
                <Button
                    component="a"
                    href={pending.authUrl}
                    rel="noopener"
                    size="compact-md"
                    target="_blank"
                    variant="filled"
                >
                    Open Last.fm
                </Button>
                <Button onClick={() => complete(false)} size="compact-md" variant="default">
                    I've approved it
                </Button>
                <Button onClick={cancel} size="compact-md" variant="subtle">
                    Cancel
                </Button>
            </Group>
        );
    } else {
        description = `Send the music you play to your own Last.fm profile. Only what's played while signed in as ${account} is sent: other people's listening on this server stays theirs.`;
        control = (
            <Button disabled={busy} onClick={begin} size="compact-md" variant="filled">
                Connect Last.fm
            </Button>
        );
    }

    return (
        <SettingsSection
            extra={
                error ? (
                    <Text size="sm" style={{ color: 'var(--theme-colors-state-error)' }}>
                        {error}
                    </Text>
                ) : null
            }
            options={[{ control, description, title: 'Last.fm' }]}
            title="Scrobbling"
        />
    );
};

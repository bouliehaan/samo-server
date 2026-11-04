import { useEffect } from 'react';

import { subscribePlayerVolume, usePlayerStore } from '/@/renderer/store';

const TOUCH =
    typeof window !== 'undefined' &&
    Boolean(window.matchMedia?.('(hover: none) and (pointer: coarse)').matches);

/**
 * On a phone, samo plays at full level and the phone's own buttons set the
 * volume, as every phone player does.
 *
 * The player starts at 30 of 100, and the engine squares it (0.3² ≈ 9%, some
 * 21 dB down) for a curve that suits a desktop slider. The phone layout has no
 * slider, so a phone stayed at that level with no way up but to raise the
 * phone's own volume to the top and still find it quiet. Safari on an iPhone
 * ignores a page's volume, which is why it showed on Android first.
 *
 * Held rather than set once: the stored volume is read back from IndexedDB
 * after the layout has mounted, and would put the 30 back.
 */
export const usePhoneFullVolume = () => {
    useEffect(() => {
        if (!TOUCH) return undefined;
        const pin = () => {
            const state = usePlayerStore.getState();
            if (state.player.volume !== 100) state.setVolume(100);
            if (state.player.muted) state.mediaToggleMute();
        };
        pin();
        return subscribePlayerVolume(pin);
    }, []);
};

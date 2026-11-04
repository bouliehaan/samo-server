import clsx from 'clsx';
import { MouseEvent } from 'react';

import styles from './samo-chrome.module.css';

import {
    CenterPlayButton,
    NextButton,
    RadioCenterPlayButton,
} from '/@/renderer/features/player/components/center-controls';
import { useIsRadioActive } from '/@/renderer/features/radio/hooks/use-radio-player';
import { useNowPlaying } from '/@/renderer/hooks/use-now-playing';
import {
    useFullScreenPlayerStoreActions,
    usePlayerSong,
    usePlayerSongProperties,
    usePlayerTimestamp,
    useSetFullScreenPlayerStore,
} from '/@/renderer/store';
import { useAudiobookDuration, useAudiobookPosition } from '/@/renderer/store/audiobook.store';
import { usePlaybackSource } from '/@/renderer/store/playback-owner.store';
import { usePodcastDuration, usePodcastPosition } from '/@/renderer/store/podcast.store';
import { Icon } from '/@/shared/components/icon/icon';
import { PlaybackSelectors } from '/@/shared/constants/playback-selectors';

const Progress = () => {
    const source = usePlaybackSource();
    const timestamp = usePlayerTimestamp();
    const song = usePlayerSong();
    const audiobookPosition = useAudiobookPosition();
    const audiobookDuration = useAudiobookDuration();
    const podcastPosition = usePodcastPosition();
    const podcastDuration = usePodcastDuration();

    let position = 0;
    let duration = 0;
    if (source === 'audiobook') {
        position = audiobookPosition;
        duration = audiobookDuration;
    } else if (source === 'podcast') {
        position = podcastPosition;
        duration = podcastDuration;
    } else if (source !== 'radio' && song?.duration) {
        position = timestamp;
        duration = song.duration / 1000;
    }
    if (!duration) return null;
    const fraction = Math.min(1, Math.max(0, position / duration));

    return (
        <div aria-hidden className={styles.miniProgress}>
            <div className={styles.miniProgressFill} style={{ transform: `scaleX(${fraction})` }} />
        </div>
    );
};

/**
 * What is playing, above the tabs: cover, title over artist, play/pause, and
 * next where there is a next. Tapping anywhere else opens the full player.
 * Works for all four kinds — the renderer's own phone bar read only the music
 * queue, so an audiobook or a podcast showed as two dashes.
 */
export const MobileMiniPlayer = () => {
    const nowPlaying = useNowPlaying();
    const isRadioActive = useIsRadioActive();
    const { id: currentSongId } = usePlayerSongProperties(['id']) ?? {};
    const setFullScreenPlayer = useSetFullScreenPlayerStore();
    const { setStore } = useFullScreenPlayerStoreActions();

    if (!nowPlaying.title) return null;

    const isRadio = isRadioActive && currentSongId === undefined;
    const isMusic = !isRadio && nowPlaying.source !== 'audiobook' && nowPlaying.source !== 'podcast';
    const subtitle = [nowPlaying.artist, nowPlaying.subtitle]
        .filter((part, index, parts) => part && parts.indexOf(part) === index)
        .join(' · ');

    const open = () => {
        setStore({ activeTab: 'player' });
        setFullScreenPlayer({ expanded: true });
    };
    const keep = (event: MouseEvent) => event.stopPropagation();

    return (
        <div
            aria-label="Open player"
            className={clsx(styles.mini, PlaybackSelectors.mediaPlayer)}
            onClick={open}
            onKeyDown={(event) => {
                if (event.target !== event.currentTarget) return;
                if (event.key === 'Enter' || event.key === ' ') open();
            }}
            role="button"
            tabIndex={0}
        >
            <Progress />
            <div className={styles.miniArt}>
                {nowPlaying.artwork ? (
                    <img alt="" className={PlaybackSelectors.playerCoverArt} src={nowPlaying.artwork} />
                ) : (
                    <Icon color="muted" icon={isRadio ? 'radio' : 'itemAlbum'} size="lg" />
                )}
            </div>
            <div className={styles.miniText}>
                <div className={clsx(styles.miniTitle, PlaybackSelectors.songTitle)}>
                    {nowPlaying.title}
                </div>
                {subtitle && (
                    <div className={clsx(styles.miniSubtitle, PlaybackSelectors.songArtist)}>
                        {subtitle}
                    </div>
                )}
            </div>
            <div className={styles.miniControls} onClick={keep}>
                {isRadio ? <RadioCenterPlayButton /> : <CenterPlayButton />}
                {isMusic && <NextButton />}
            </div>
        </div>
    );
};

import { useNowPlaying } from '/@/renderer/hooks/use-now-playing';
import { useAudiobookDuration, useAudiobookPosition } from '/@/renderer/store/audiobook.store';
import { usePlaybackSource } from '/@/renderer/store/playback-owner.store';
import { usePodcastDuration, usePodcastPosition } from '/@/renderer/store/podcast.store';

/**
 * The phone's full-screen player read only the music queue, so an audiobook
 * or a podcast opened to a blank cover, dashes for its title and 0:00 / 0:00
 * under a seek bar that could not seek. These give its pieces the same
 * per-source answers the desktop player bar already uses.
 */

const isLongForm = (source: null | string) => source === 'audiobook' || source === 'podcast';

/** Position in seconds: the long-form store's when one is playing. */
export const useSamoPlaybackTime = (musicTime: number) => {
    const source = usePlaybackSource();
    const audiobook = useAudiobookPosition();
    const podcast = usePodcastPosition();
    if (source === 'audiobook') return audiobook;
    if (source === 'podcast') return podcast;
    return musicTime;
};

/** Length in seconds: the long-form store's when one is playing. */
export const useSamoPlaybackDuration = (musicDuration: number) => {
    const source = usePlaybackSource();
    const audiobook = useAudiobookDuration();
    const podcast = usePodcastDuration();
    if (source === 'audiobook') return audiobook;
    if (source === 'podcast') return podcast;
    return musicDuration;
};

/** Title, author and chapter (or show) for an audiobook or podcast; null for music and radio. */
export const useSamoLongFormMeta = () => {
    const nowPlaying = useNowPlaying();
    if (!isLongForm(nowPlaying.source)) return null;
    return { artist: nowPlaying.artist, subtitle: nowPlaying.subtitle, title: nowPlaying.title };
};

/** Cover for anything that is not the music queue: a book, a show, a station. */
export const useSamoSourceArtwork = () => {
    const nowPlaying = useNowPlaying();
    if (!isLongForm(nowPlaying.source) && nowPlaying.source !== 'radio') return undefined;
    return nowPlaying.artwork || undefined;
};

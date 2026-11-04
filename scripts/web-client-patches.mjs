// Hosted-browser adaptations are applied only to the temporary renderer copy.
// Fail on upstream drift rather than silently shipping an unapplied fix.
import { readFile, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
export async function patchWebClient(stage) {
  async function replace(path, before, after) {
    const file = join(stage, path);
    const source = await readFile(file, 'utf8');
    if (source.split(before).length !== 2) throw new Error(`Browser patch no longer matches: ${path}`);
    await writeFile(file, source.replace(before, after));
  }
  const engine = 'src/renderer/features/player/audio-player/engine/web-player-engine.tsx';
  // The idle second deck must never retry its placeholder and pause the live deck.
  await replace(engine, 'if (!player || !(target instanceof HTMLAudioElement)) {',
    'if (!sourceRef.current || !player || !(target instanceof HTMLAudioElement)) {');
  await replace(engine, '? player1Ref.current?.seekTo(seekTo)\n                : player2Ref.current?.seekTo(seekTo);',
    "? player1Ref.current?.seekTo(seekTo, 'seconds')\n                : player2Ref.current?.seekTo(seekTo, 'seconds');");
  // Whole-file podcast streams retain container headers and support native byte
  // ranges in both directions. Resume is performed by the browser player.
  await replace('src/renderer/api/samo/samo-long-form.ts',
    '    const resume = Math.max(0, Math.floor(progressSeconds ?? 0));\n', '');
  await replace('src/renderer/api/samo/samo-long-form.ts',
    '        ...(resume > 0 ? { offsetSeconds: resume } : {}),\n', '');
  // The S menu carries the hosted player's own items from bootstrap.js:
  // Server admin (admins only) and Sign out, after the menu's own.
  await replace('src/renderer/features/titlebar/components/app-menu.tsx',
    "        {\n            condition: isElectron(),\n            id: 'quit',",
    "        ...((window as any).SAMO_HOST_MENU ?? []).map((entry: any) => ({ ...entry, type: 'item' as const })),\n        {\n            condition: isElectron(),\n            id: 'quit',");
  // The browser already has back and forward; the shell's own pair is for Electron.
  const mainContent = 'src/renderer/layouts/default-layout/main-content.tsx';
  await replace(mainContent, "import { useNavigate } from 'react-router';\n", '');
  await replace(mainContent, `    const navigate = useNavigate();

    return (
        <>
            <div className={styles['chrome-navigation-controls']}>
                <button
                    aria-label="Back"
                    className={styles['chrome-shell-button']}
                    onClick={() => navigate(-1)}
                    type="button"
                >
                    <Icon icon="arrowLeftS" size="lg" />
                </button>
                <button
                    aria-label="Forward"
                    className={styles['chrome-shell-button']}
                    onClick={() => navigate(1)}
                    type="button"
                >
                    <Icon icon="arrowRightS" size="lg" />
                </button>
            </div>

`, `    return (
        <>
`);
  // This is a single-server player over the complete library, with no folder setup.
  await writeFile(join(stage, 'src/renderer/features/sidebar/components/server-selector.tsx'),
    'export const ServerSelector = () => null;\n');
  const search = 'src/renderer/features/search/hooks/use-unified-search.ts';
  await replace(search, "const ENTITY_TYPE_BUMP = 50;", "const ENTITY_TYPE_BUMP = 50;\nimport { artistNameBonus } from '/@/renderer/server-search-ranking.js';");
  await replace(search, "scoreCandidate(artist.name, [], ctx.needle, ctx.tokens) +\n                entityBumpFor('artists', ctx)",
    "scoreCandidate(artist.name, [], ctx.needle, ctx.tokens) +\n                artistNameBonus(artist.name, ctx.needle) + entityBumpFor('artists', ctx)");
  await replace(search, '.sort((a, b) => b.score - a.score || a.artist.name.localeCompare(b.artist.name));',
    '.sort((a, b) => b.score - a.score || (b.artist.albumCount ?? 0) - (a.artist.albumCount ?? 0) || a.artist.name.localeCompare(b.artist.name));');
  await patchPhone(stage, replace);
}

// The phone. The components themselves live in web/client/mobile, copied to
// src/renderer/server-mobile; these are the hooks into the renderer for them,
// and the touch fixes that have to be made where the behaviour is.
async function patchPhone(stage, replace) {
  // The phone layout (top bar, tabs, mini player, menu) replaces the renderer's
  // own, which was a floating menu button over the page. Renaming rather than
  // overwriting keeps the drift check: if upstream reshapes its layout, this
  // stops matching and someone looks at what changed.
  const layout = 'src/renderer/layouts/mobile-layout/mobile-layout.tsx';
  await replace(layout, 'export const MobileLayout = ({ shell }: MobileLayoutProps) => {', 'const UpstreamMobileLayout = ({ shell }: MobileLayoutProps) => {');
  await writeFile(join(stage, layout), (await readFile(join(stage, layout), 'utf8')) +
    "\nexport { MobileLayout } from '/@/renderer/server-mobile/mobile-layout';\n");
  // A phone on its side is still a phone: 844×390 got the desktop sidebar.
  await replace('src/renderer/hooks/use-is-mobile.ts', "useMediaQuery('(max-width: 768px)')",
    "useMediaQuery('(max-width: 768px), (pointer: coarse) and (max-height: 500px)')");
  // The mini player uses the desktop bar's own play and next buttons, which
  // already know how to start a podcast, resume an audiobook or skip a channel.
  const controls = 'src/renderer/features/player/components/center-controls.tsx';
  for (const name of ['CenterPlayButton', 'NextButton', 'RadioCenterPlayButton']) {
    await replace(controls, `\nconst ${name} = (`, `\nexport const ${name} = (`);
  }

  // A tap plays. The lists open albums and play songs on a double click and
  // only select on a single one, so on a phone a tap on a song highlighted it
  // and nothing else; a double tap is not a gesture anyone makes. A touch now
  // does what the double click does, unless it landed on a control in the row
  // (its heart, its ⋯), which keeps its own meaning.
  const doubleClick = 'src/shared/hooks/use-double-click.ts';
  await replace(doubleClick, "import { useCallback, useEffect, useRef } from 'react';\n",
    "import { useCallback, useEffect, useRef } from 'react';\n\n" +
    "const COARSE = typeof window !== 'undefined' && Boolean(window.matchMedia?.('(hover: none) and (pointer: coarse)').matches);\n" +
    "const CONTROL = 'button, a, input, select, textarea, label, [role=\"button\"], [role=\"checkbox\"], [role=\"switch\"], [role=\"menuitem\"]';\n" +
    "const isTouch = (e: any) => {\n" +
    "    const type = e?.nativeEvent?.pointerType;\n" +
    "    if (type === 'touch' || type === 'pen') return true;\n" +
    "    if (type === 'mouse') return false;\n" +
    "    return COARSE;\n" +
    "};\n");
  await replace(doubleClick, "        (e: any) => {\n            clickCountRef.current += 1;",
    "        (e: any) => {\n" +
    "            if (isTouch(e)) {\n" +
    "                const control = (e?.target as HTMLElement | null)?.closest?.(CONTROL);\n" +
    "                const inside = control && control !== e.currentTarget && e.currentTarget?.contains?.(control);\n" +
    "                if (inside) onSingleClick(e);\n" +
    "                else onDoubleClick(e);\n" +
    "                return;\n" +
    "            }\n" +
    "            clickCountRef.current += 1;");

  // A tap is a hover on a touch screen: the tooltip it opened stayed over the
  // page (the "Menu" label sat on top of every album) until the next tap.
  const tooltip = 'src/shared/components/tooltip/tooltip.tsx';
  await replace(tooltip, 'const DEFAULT_TRANSITION_PROPS = {',
    "const NO_HOVER = typeof window !== 'undefined' && Boolean(window.matchMedia?.('(hover: none)').matches);\n\nconst DEFAULT_TRANSITION_PROPS = {");
  await replace(tooltip, '                withinPortal={withinPortal}\n                {...props}\n            >',
    '                withinPortal={withinPortal}\n                {...props}\n                disabled={NO_HOVER || props.disabled}\n            >');

  // Song tables keep only cover, title/artist, length and ⋯ on a phone.
  const table = 'src/renderer/components/item-list/item-table-list/item-table-list.tsx';
  await replace(table, "import { parseTableColumns } from '/@/renderer/components/item-list/helpers/parse-table-columns';\n",
    "import { parseTableColumns } from '/@/renderer/components/item-list/helpers/parse-table-columns';\nimport { useSamoPhoneColumns } from '/@/renderer/server-mobile/phone-columns';\n");
  await replace(table,
    '    const columnsForLayout = useMemo(\n        () => appendLayoutFillColumn(columns, autoFitColumns),\n        [autoFitColumns, columns],\n    );',
    '    const samoPhoneColumns = useSamoPhoneColumns(columns);\n    const fitColumns = autoFitColumns || Boolean(samoPhoneColumns);\n' +
    '    const columnsForLayout = useMemo(\n        () => samoPhoneColumns ?? appendLayoutFillColumn(columns, autoFitColumns),\n        [autoFitColumns, columns, samoPhoneColumns],\n    );');
  await replace(table, '    } = useTableColumnModel({\n        autoFitColumns,\n',
    '    } = useTableColumnModel({\n        autoFitColumns: fitColumns,\n');
  await replace(table, '    useContainerWidthTracking({\n        autoFitColumns,\n',
    '    useContainerWidthTracking({\n        autoFitColumns: fitColumns,\n');

  // Context menus: a submenu ("Play ›", "Add to queue ›", "Go to ›") opened
  // only under a hovering mouse. A tap ran the row's own action instead, and
  // "Go to" has none, so album and artist were out of reach on a phone. On a
  // touch, the tap opens the submenu; the mouse keeps hover-to-open and
  // click-for-the-default.
  // The mouse events a browser makes up after a tap are not a hover either: the
  // leave that follows closed the submenu 150ms after it opened. On a screen
  // with no hover they are ignored, and opening one submenu closes the last
  // (there are no nested ones to keep open).
  const contextMenu = 'src/shared/components/context-menu/context-menu.tsx';
  await replace(contextMenu, 'const SubmenuContext = createContext<null | SubmenuContext>(null);\n',
    "const SAMO_NO_HOVER = typeof window !== 'undefined' && Boolean(window.matchMedia?.('(hover: none)').matches);\n" +
    'let samoTouchSubmenu: Dispatch<SetStateAction<boolean>> | null = null;\n\n' +
    'const SubmenuContext = createContext<null | SubmenuContext>(null);\n');
  await replace(contextMenu, '    const handleMouseLeave = () => {\n        if (isCloseDisabled) {',
    '    const handleMouseLeave = () => {\n        if (SAMO_NO_HOVER) return;\n        if (isCloseDisabled) {');
  await replace(contextMenu, '    const handleMouseEnter = () => {\n        if (disabled) return;',
    '    const handleMouseEnter = () => {\n        if (disabled || SAMO_NO_HOVER) return;');
  await replace(contextMenu, '    const handleMouseLeave = () => {\n        if (openTimeoutRef.current) {',
    '    const handleMouseLeave = () => {\n        if (SAMO_NO_HOVER) return;\n        if (openTimeoutRef.current) {');
  await replace(contextMenu,
    '        <RadixContextMenu.SubTrigger\n            className={clsx({ [styles.disabled]: disabled })}\n            disabled={disabled}\n            onMouseEnter={handleMouseEnter}',
    '        <RadixContextMenu.SubTrigger\n            className={clsx({ [styles.disabled]: disabled })}\n            disabled={disabled}\n' +
    '            onClickCapture={(event) => {\n' +
    "                const type = (event.nativeEvent as PointerEvent).pointerType;\n" +
    "                const touch = type === 'touch' || type === 'pen' || (type !== 'mouse' && SAMO_NO_HOVER);\n" +
    '                if (!touch || disabled) return;\n' +
    '                event.preventDefault();\n' +
    '                event.stopPropagation();\n' +
    '                cancelCloseTimeout();\n' +
    '                if (samoTouchSubmenu && samoTouchSubmenu !== setOpen) samoTouchSubmenu(false);\n' +
    '                samoTouchSubmenu = setOpen;\n' +
    '                setOpen(true);\n' +
    '            }}\n' +
    '            onMouseEnter={handleMouseEnter}');

  // The row's ⋯ only appeared under a hovering mouse, so a touch screen never
  // showed it. Without hover it is always there.
  await replace('src/renderer/components/item-list/item-table-list/columns/actions-column.tsx',
    '                    className="hover-only"\n',
    "                    className={window.matchMedia?.('(hover: none)').matches ? undefined : 'hover-only'}\n");

  // Settings: each label had half the row whatever the control beside it, so
  // on a phone "Sets the language for the application" wrapped a word a line.
  // The text now takes what the control leaves, and the control drops below it
  // when there is not room for both.
  const option = 'src/renderer/features/settings/components/settings-option.tsx';
  await replace(option, "import { Tooltip } from '/@/shared/components/tooltip/tooltip';\n",
    "import { Tooltip } from '/@/shared/components/tooltip/tooltip';\n\nconst PHONE = typeof window !== 'undefined' && Boolean(window.matchMedia?.('(max-width: 768px)').matches);\n");
  await replace(option, ' wrap="nowrap">', " wrap={PHONE ? 'wrap' : 'nowrap'}>");
  await replace(option, "                            maxWidth: '50%',\n",
    "                            flex: PHONE ? '1 1 12rem' : undefined,\n                            maxWidth: PHONE ? undefined : '50%',\n");

  // Audiobook and podcast shelves: two covers to a row, not one the width of
  // the screen. The gap stays 20px, which the grid's stylesheet also assumes.
  await replace('src/renderer/features/long-form/components/long-form-grid.tsx', 'const MIN_TILE_WIDTH = 170;',
    "const MIN_TILE_WIDTH = typeof window !== 'undefined' && window.matchMedia?.('(max-width: 768px)').matches ? 130 : 170;");

  // The full-screen player read only the music queue: an audiobook or podcast
  // opened to a blank cover, dashes for a title and 0:00 / 0:00. Its title,
  // cover and progress now follow whatever is playing (server-mobile/long-form).
  const fullPlayer = 'src/renderer/features/player/components/mobile-fullscreen-player';
  const longForm = "import { useSamoLongFormMeta, useSamoPlaybackDuration, useSamoPlaybackTime, useSamoSourceArtwork } from '/@/renderer/server-mobile/long-form';\n";
  const metadata = `${fullPlayer}-metadata.tsx`;
  await replace(metadata, "import { PlaybackSelectors } from '/@/shared/constants/playback-selectors';\n",
    "import { PlaybackSelectors } from '/@/shared/constants/playback-selectors';\n" + longForm);
  await replace(metadata, '        const isRadio = radioTitle !== undefined || radioStationName !== undefined;\n',
    '        const isRadio = radioTitle !== undefined || radioStationName !== undefined;\n        const samoLongForm = useSamoLongFormMeta();\n');
  await replace(metadata, '        const title = isRadio ?', '        const title = samoLongForm ? samoLongForm.title : isRadio ?');
  await replace(metadata, '        const artistsDisplay = isRadio\n', '        const artistsDisplay = samoLongForm ? samoLongForm.artist : isRadio\n');
  await replace(metadata, '        const album = isRadio ?', '        const album = samoLongForm ? samoLongForm.subtitle : isRadio ?');
  await replace(metadata, '        const hasMetadata = !isRadio && (', '        const hasMetadata = !isRadio && !samoLongForm && (');
  // The heart favorites the current song, which a book does not have.
  await replace(metadata, '                {!isRadio && (', '                {!isRadio && !samoLongForm && (');
  const progress = `${fullPlayer}-progress.tsx`;
  await replace(progress, "import { PlaybackSelectors } from '/@/shared/constants/playback-selectors';\n",
    "import { PlaybackSelectors } from '/@/shared/constants/playback-selectors';\n" + longForm);
  await replace(progress, '        const currentTime = usePlayerTimestamp();', '        const currentTime = useSamoPlaybackTime(usePlayerTimestamp());');
  await replace(progress, '        const songDuration = currentSong?.duration ? currentSong.duration / 1000 : 0;',
    '        const songDuration = useSamoPlaybackDuration(currentSong?.duration ? currentSong.duration / 1000 : 0);');
  // The waveform is drawn from the music track's file.
  await replace(progress, '        const isWaveform = !isRadioMode && playerbarSlider',
    "        const isWaveform = !isRadioMode && source !== 'audiobook' && source !== 'podcast' && playerbarSlider");
  const art = `${fullPlayer}-album-art.tsx`;
  await replace(art, "import { PlaybackSelectors } from '/@/shared/constants/playback-selectors';\n",
    "import { PlaybackSelectors } from '/@/shared/constants/playback-selectors';\n" + longForm);
  await replace(art, '    const isPlayingRadio = isRadioActive && isRadioPlaying;\n',
    '    const isPlayingRadio = isRadioActive && isRadioPlaying;\n    const samoSourceArt = useSamoSourceArtwork();\n');
  await replace(art, '                    {isPlayingRadio ? (',
    '                    {samoSourceArt ? (\n' +
    '                        <ImageWithPlaceholder animate="open" className={PlaybackSelectors.playerCoverArt} custom={{ isOpen: true }}\n' +
    '                            draggable={false} exit="closed" initial="closed" key={`samo-${samoSourceArt}`} loading="eager"\n' +
    '                            src={samoSourceArt} useImageAspectRatio={useImageAspectRatio} variants={imageVariants} />\n' +
    '                    ) : isPlayingRadio ? (');

  // Playlists open as covers on a phone, as on the Android tab, rather than as
  // a list of names. Only the starting value: the grid/table toggle still works
  // and is remembered.
  await replace('src/renderer/store/settings/defaults.ts',
    '        [LibraryItem.PLAYLIST]: {\n            display: ListDisplayType.TABLE,\n',
    "        [LibraryItem.PLAYLIST]: {\n            display: typeof window !== 'undefined' && window.matchMedia?.('(max-width: 768px)').matches ? ListDisplayType.GRID : ListDisplayType.TABLE,\n");

  // A link can open the radio page on one of its filters (?filter=internet,
  // the phone menu's Internet Radio). The page stays mounted between visits,
  // so a later link sets the filter as well as the first one.
  const radio = 'src/renderer/features/radio/routes/radio-list-route.tsx';
  await replace(radio, "import { useMemo, useState } from 'react';\n",
    "import { useEffect, useMemo, useState } from 'react';\nimport { useLocation, useSearchParams } from 'react-router';\n");
  await replace(radio, "    const [filter, setFilter] = useState<RadioFilter>('all');\n",
    "    const [searchParams] = useSearchParams();\n" +
    "    const { key: visit } = useLocation();\n" +
    "    const requested = searchParams.get('filter');\n" +
    "    const linked = (['all', 'channels', 'favorites', 'internet'] as const).find((id) => id === requested);\n" +
    "    const [filter, setFilter] = useState<RadioFilter>(linked ?? 'all');\n" +
    "    useEffect(() => {\n" +
    "        if (linked) setFilter(linked);\n" +
    "    }, [linked, visit]);\n");

  // Scrobbling heads the General tab, so a listener can link their own Last.fm
  // account without the admin dashboard (web/client/settings).
  const general = 'src/renderer/features/settings/components/general/general-tab.tsx';
  await replace(general, "import { Stack } from '/@/shared/components/stack/stack';\n",
    "import { Stack } from '/@/shared/components/stack/stack';\nimport { ScrobblingSettings } from '/@/renderer/server-settings/scrobbling-settings';\n");
  await replace(general, '        <Stack gap="md">\n            <ApplicationSettings />',
    '        <Stack gap="md">\n            <ScrobblingSettings />\n            <ApplicationSettings />');

  // "Search music, audiobooks, po" is what fitted of the placeholder.
  await replace('src/renderer/features/search/components/global-search-bar.tsx',
    "                        placeholder={t('page.sidebar.searchPlaceholder', {",
    "                        placeholder={window.matchMedia?.('(max-width: 768px)').matches ? t('common.search', { postProcess: 'titleCase' }) : t('page.sidebar.searchPlaceholder', {");

  // iOS: no Web Audio graph (see bootstrap.js). Every engine checks for the
  // context before routing through it, so not making one is the whole change.
  await replace('src/renderer/features/player/components/audio-players.tsx',
    "        if (webAudio && 'AudioContext' in window) {",
    "        if (webAudio && 'AudioContext' in window && !(window as any).SAMO_NO_WEB_AUDIO) {");
}

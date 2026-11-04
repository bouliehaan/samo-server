import { AnimatePresence } from 'motion/react';
import { Suspense, useEffect } from 'react';
import { Outlet } from 'react-router';

import styles from './samo-layout.module.css';
import './mobile-overrides.css';

import { MobileMenu } from '/@/renderer/server-mobile/mobile-menu';
import { MobileMiniPlayer } from '/@/renderer/server-mobile/mobile-mini-player';
import { MobileTabBar } from '/@/renderer/server-mobile/mobile-tab-bar';
import { MobileTopBar } from '/@/renderer/server-mobile/mobile-top-bar';
import { ContextMenuController } from '/@/renderer/features/context-menu/context-menu-controller';
import { FullScreenVisualizer } from '/@/renderer/features/player/components/full-screen-visualizer';
import { MobileFullscreenPlayer } from '/@/renderer/features/player/components/mobile-fullscreen-player';
import { RouteFallback } from '/@/renderer/features/shared/components/page-skeletons/route-fallback';
import { SectionOutlet } from '/@/renderer/layouts/default-layout/section-outlet';
import { useFullScreenPlayerOverlayState } from '/@/renderer/store';
import { useDisclosure } from '/@/shared/hooks/use-disclosure';

interface MobileLayoutProps {
    shell?: boolean;
}

/**
 * The hosted player on a phone, laid out the way the Android app is: a bar
 * with back, search and the menu along the top, the page, the mini player, and
 * the five tabs along the bottom. It replaces the desktop renderer's mobile
 * layout, which was a floating menu button over the page and nothing else.
 *
 * The five section roots stay mounted between tab switches (SectionOutlet, as
 * on the desktop), so going back to a tab keeps its scroll position.
 */
export const MobileLayout = ({ shell }: MobileLayoutProps) => {
    const [menuOpened, { close: closeMenu, open: openMenu }] = useDisclosure(false);
    const { expanded: isPlayerExpanded, visualizerExpanded } = useFullScreenPlayerOverlayState();

    // Global phone overrides of the renderer's own components hang off this,
    // so a desktop-width window is untouched by them.
    useEffect(() => {
        document.documentElement.dataset.samoMobile = '';
        return () => {
            delete document.documentElement.dataset.samoMobile;
        };
    }, []);

    if (shell) {
        return (
            <main className={styles.shell}>
                <Suspense fallback={<RouteFallback />}>
                    <Outlet />
                </Suspense>
            </main>
        );
    }

    return (
        <>
            <div className={styles.layout} id="mobile-layout">
                <MobileTopBar onOpenMenu={openMenu} />
                <main className={styles.main}>
                    <SectionOutlet />
                </main>
                <MobileMiniPlayer />
                <MobileTabBar />
            </div>
            <MobileMenu onClose={closeMenu} opened={menuOpened} />
            <AnimatePresence initial={false}>
                {isPlayerExpanded && (
                    <div className={styles.overlay}>
                        <MobileFullscreenPlayer />
                    </div>
                )}
            </AnimatePresence>
            <AnimatePresence initial={false}>
                {visualizerExpanded && (
                    <div className={styles.overlay}>
                        <FullScreenVisualizer />
                    </div>
                )}
            </AnimatePresence>
            <ContextMenuController.Root />
        </>
    );
};

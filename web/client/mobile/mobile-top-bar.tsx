import { useEffect, useRef, useState } from 'react';
import { useLocation, useNavigate } from 'react-router';

import styles from './samo-chrome.module.css';

import { backTarget, isRootPath } from '/@/renderer/server-mobile/routes.js';
import { GlobalSearchBar } from '/@/renderer/features/search/components/global-search-bar';
import { Icon } from '/@/shared/components/icon/icon';

interface MobileTopBarProps {
    onOpenMenu: () => void;
}

/** Whether the search bar's results are showing (it renders them as a listbox). */
const useResultsOpen = (ref: React.RefObject<HTMLElement | null>) => {
    const [open, setOpen] = useState(false);
    useEffect(() => {
        const element = ref.current;
        if (!element) return undefined;
        const update = () => setOpen(Boolean(element.querySelector('[role="listbox"]')));
        const observer = new MutationObserver(update);
        observer.observe(element, { childList: true, subtree: true });
        update();
        return () => observer.disconnect();
    }, [ref]);
    return open;
};

/**
 * Back (below a tab's own page), the same search across music, audiobooks,
 * podcasts and radio that the desktop has across its top, and the menu.
 * A home-screen web app has no browser chrome at all, so the back button here
 * is the only one there is.
 *
 * While results are up they cover the page, so the bar offers Cancel in place
 * of the menu: the desktop closes them on a click outside, and here there is no
 * outside left to tap.
 */
export const MobileTopBar = ({ onOpenMenu }: MobileTopBarProps) => {
    const location = useLocation();
    const navigate = useNavigate();
    const ref = useRef<HTMLElement | null>(null);
    const resultsOpen = useResultsOpen(ref);
    const showBack = !resultsOpen && !isRootPath(location.pathname);

    const handleBack = () => {
        const target = backTarget(location.pathname, location.key);
        if (target === -1) navigate(-1);
        else navigate(target);
    };

    // The search bar closes its results, and drops the keyboard, on Escape.
    const handleCancel = () => {
        document.dispatchEvent(new KeyboardEvent('keydown', { bubbles: true, key: 'Escape' }));
        (document.activeElement as HTMLElement | null)?.blur?.();
    };

    return (
        <header className={styles.topBar} ref={ref}>
            {showBack && (
                <button
                    aria-label="Back"
                    className={styles.iconButton}
                    onClick={handleBack}
                    type="button"
                >
                    <Icon icon="arrowLeftS" size="xl" />
                </button>
            )}
            <GlobalSearchBar className={styles.search} />
            {resultsOpen ? (
                <button className={styles.textButton} onClick={handleCancel} type="button">
                    Cancel
                </button>
            ) : (
                <button
                    aria-label="Menu"
                    className={styles.iconButton}
                    onClick={onOpenMenu}
                    type="button"
                >
                    <Icon icon="menu" size="lg" />
                </button>
            )}
        </header>
    );
};

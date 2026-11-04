import { SAMO_MOBILE_TABS } from '@samo/core/navigation';
import clsx from 'clsx';
import { useLocation, useNavigate } from 'react-router';

import styles from './samo-chrome.module.css';

import { TAB_ROUTES, tabForPath } from '/@/renderer/server-mobile/routes.js';
import { SidebarIcon } from '/@/renderer/features/sidebar/components/sidebar-icon';

const scrollPageToTop = () => {
    // Pages scroll inside their own containers; take whichever has moved.
    const main = document.querySelector('#mobile-layout main');
    main?.querySelectorAll<HTMLElement>('*').forEach((element) => {
        if (element.scrollTop > 0 && element.checkVisibility?.() !== false) {
            element.scrollTo({ behavior: 'smooth', top: 0 });
        }
    });
};

/**
 * The Android app's five tabs. Tapping the tab you are already in goes back
 * to its first page, and on that page scrolls it to the top: the usual
 * phone behaviour for both.
 */
export const MobileTabBar = () => {
    const location = useLocation();
    const navigate = useNavigate();
    const active = tabForPath(location.pathname);

    return (
        <nav aria-label="Sections" className={styles.tabBar}>
            {SAMO_MOBILE_TABS.map((tab) => {
                const route = TAB_ROUTES[tab.id as keyof typeof TAB_ROUTES];
                const isActive = active === tab.id;
                return (
                    <button
                        aria-current={isActive ? 'page' : undefined}
                        className={clsx(styles.tab, isActive && styles.tabActive)}
                        key={tab.id}
                        onClick={() => {
                            if (location.pathname === route) scrollPageToTop();
                            else navigate(route);
                        }}
                        type="button"
                    >
                        <span className={styles.tabIcon}>
                            <SidebarIcon active={isActive} route={route} size="1.5rem" />
                        </span>
                        <span className={styles.tabLabel}>{tab.label}</span>
                    </button>
                );
            })}
        </nav>
    );
};

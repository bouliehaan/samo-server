import { useTranslation } from 'react-i18next';
import { useNavigate } from 'react-router';

import styles from './samo-chrome.module.css';

import { SidebarIcon } from '/@/renderer/features/sidebar/components/sidebar-icon';
import { AppRoute } from '/@/renderer/router/routes';
import { Drawer } from '/@/shared/components/drawer/drawer';
import { Icon } from '/@/shared/components/icon/icon';

interface HostMenuEntry {
    icon: string;
    id: string;
    label: string;
    onClick: () => void;
}

interface MobileMenuProps {
    onClose: () => void;
    opened: boolean;
}

/**
 * Everything the five tabs do not reach: the music library by kind, favorites,
 * settings, and the hosted player's own entries from bootstrap.js (server
 * admin for admins, and signing out). The desktop sidebar it replaces opened
 * on a blank button where its logo should have been, so "sign out" was hidden
 * behind something that did not look like a button at all.
 */
export const MobileMenu = ({ onClose, opened }: MobileMenuProps) => {
    const { t } = useTranslation();
    const navigate = useNavigate();
    const host = (window as any).SAMO_HOST_USER as undefined | { isAdmin: boolean; name: string };
    const hostEntries = ((window as any).SAMO_HOST_MENU ?? []) as HostMenuEntry[];

    const library = [
        { label: t('page.sidebar.music', { defaultValue: 'Music', postProcess: 'titleCase' }), route: AppRoute.MUSIC },
        { label: t('page.sidebar.albums', { postProcess: 'titleCase' }), route: AppRoute.LIBRARY_ALBUMS },
        { label: t('page.sidebar.albumArtists', { postProcess: 'titleCase' }), route: AppRoute.LIBRARY_ALBUM_ARTISTS },
        { label: t('page.sidebar.tracks', { postProcess: 'titleCase' }), route: AppRoute.LIBRARY_SONGS },
        { label: t('page.sidebar.genres', { postProcess: 'titleCase' }), route: AppRoute.LIBRARY_GENRES },
        { label: t('page.sidebar.favorites', { postProcess: 'titleCase' }), route: AppRoute.FAVORITES },
    ];

    const go = (route: string) => {
        onClose();
        navigate(route);
    };

    return (
        <Drawer
            classNames={{ body: styles.menuBody, content: styles.menuContent }}
            onClose={onClose}
            opened={opened}
            position="left"
            size="min(320px, 86vw)"
            withCloseButton={false}
        >
            <div className={styles.menuHeader}>
                <span className={styles.menuBrand}>samo</span>
                {host?.name && <span className={styles.menuUser}>{host.name}</span>}
            </div>
            <div className={styles.menuSection}>
                {t('page.sidebar.myLibrary', { postProcess: 'titleCase' })}
            </div>
            {library.map((item) => (
                <button
                    className={styles.menuItem}
                    key={item.route}
                    onClick={() => go(item.route)}
                    type="button"
                >
                    <span className={styles.menuIcon}>
                        <SidebarIcon active={false} route={item.route} size="1.25rem" />
                    </span>
                    {item.label}
                </button>
            ))}
            <div className={styles.menuDivider} />
            <button
                className={styles.menuItem}
                onClick={() => go(AppRoute.SETTINGS)}
                type="button"
            >
                <span className={styles.menuIcon}>
                    <SidebarIcon active={false} route={AppRoute.SETTINGS} size="1.25rem" />
                </span>
                {t('page.sidebar.settings', { postProcess: 'titleCase' })}
            </button>
            {hostEntries.map((entry) => (
                <button
                    className={styles.menuItem}
                    key={entry.id}
                    onClick={() => {
                        onClose();
                        entry.onClick();
                    }}
                    type="button"
                >
                    <span className={styles.menuIcon}>
                        <Icon icon={entry.icon as any} size="md" />
                    </span>
                    {entry.label}
                </button>
            ))}
        </Drawer>
    );
};

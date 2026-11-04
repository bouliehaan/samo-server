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

interface MenuEntry {
    /** Where the icon comes from, when it differs from where the entry goes. */
    icon?: string;
    label: string;
    to: string;
}

interface MobileMenuProps {
    onClose: () => void;
    opened: boolean;
}

/**
 * The rest of samo, beyond the five tabs: every kind of media first, then the
 * music library by kind, then settings and the hosted player's own entries
 * from bootstrap.js (server admin for admins, and signing out).
 */
export const MobileMenu = ({ onClose, opened }: MobileMenuProps) => {
    const { t } = useTranslation();
    const navigate = useNavigate();
    const host = (window as any).SAMO_HOST_USER as undefined | { isAdmin: boolean; name: string };
    const hostEntries = ((window as any).SAMO_HOST_MENU ?? []) as HostMenuEntry[];

    const listen: MenuEntry[] = [
        { label: t('page.sidebar.playlists', { postProcess: 'titleCase' }), to: AppRoute.PLAYLISTS },
        { label: 'Audiobooks', to: AppRoute.AUDIOBOOKS },
        // The podcast feed (latest episodes) heads the Podcasts page; the home
        // page's "Podcast Feed" links there too.
        { label: 'Podcasts', to: AppRoute.PODCASTS },
        // The radio page opens on this filter (scripts/web-client-patches.mjs).
        { icon: AppRoute.RADIO, label: 'Internet Radio', to: `${AppRoute.RADIO}?filter=internet` },
    ];
    const music: MenuEntry[] = [
        { label: t('page.sidebar.albums', { postProcess: 'titleCase' }), to: AppRoute.LIBRARY_ALBUMS },
        { label: t('page.sidebar.artists', { postProcess: 'titleCase' }), to: AppRoute.LIBRARY_ALBUM_ARTISTS },
        { label: t('page.sidebar.favorites', { postProcess: 'titleCase' }), to: AppRoute.FAVORITES },
        // The music page leads on to songs and genres, which have no other way in.
        { label: 'All Music', to: AppRoute.MUSIC },
    ];

    const go = (to: string) => {
        onClose();
        navigate(to);
    };

    const section = (title: string, entries: MenuEntry[]) => (
        <>
            <div className={styles.menuSection}>{title}</div>
            {entries.map((entry) => (
                <button
                    className={styles.menuItem}
                    key={entry.to}
                    onClick={() => go(entry.to)}
                    type="button"
                >
                    <span className={styles.menuIcon}>
                        <SidebarIcon active={false} route={entry.icon ?? entry.to} size="1.25rem" />
                    </span>
                    {entry.label}
                </button>
            ))}
        </>
    );

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
            {section('Listen', listen)}
            {section(t('entity.music', { defaultValue: 'Music', postProcess: 'titleCase' }), music)}
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

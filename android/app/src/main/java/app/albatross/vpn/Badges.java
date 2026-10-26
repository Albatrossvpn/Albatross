package app.albatross.vpn;

import android.content.ComponentName;
import android.content.Context;
import android.content.Intent;
import android.content.pm.PackageManager;

/**
 * The plan's badge on the launcher icon. The manifest has one launcher
 * entry per plan (activity-aliases of MainActivity with a badged icon),
 * off by default; the engine names the signed-in account's plan and the
 * entry for it is turned on, every other one off, MainActivity's own
 * plain entry included. Launchers redraw the icon when the entries
 * change, some only once the app is in the background, so a change
 * waits until then.
 */
final class Badges {
    static final String[] PLANS = {"bronze", "silver", "gold", "vip"};
    // The plan to show ("" for none), or null while nothing is pending.
    private static String wanted;
    private static boolean foreground;

    private Badges() {}

    /** Asks for the icon to carry this plan's badge; "" or unknown means the plain icon. */
    static synchronized void request(Context c, String tier) {
        String t = tier == null ? "" : tier;
        boolean known = false;
        for (String p : PLANS) if (p.equals(t)) known = true;
        wanted = known ? t : "";
        if (!foreground) apply(c);
    }

    /** MainActivity says whether it is on screen; a pending change is made once it is not. */
    static synchronized void setForeground(Context c, boolean fg) {
        foreground = fg;
        if (!fg && wanted != null) apply(c);
    }

    private static void apply(Context c) {
        String tier = wanted;
        wanted = null;
        PackageManager pm = c.getPackageManager();
        String pkg = c.getPackageName();
        // The chosen entry goes on first, so the app always has one.
        set(pm, pkg, tier, true);
        for (String p : PLANS) if (!p.equals(tier)) set(pm, pkg, p, false);
        if (!tier.isEmpty()) set(pm, pkg, "", false);
    }

    private static void set(PackageManager pm, String pkg, String tier, boolean on) {
        ComponentName cn = tier.isEmpty() ? new ComponentName(pkg, MainActivity.class.getName()) : new ComponentName(pkg, pkg + ".Launcher" + Character.toUpperCase(tier.charAt(0)) + tier.substring(1));
        // The manifest's default is on for MainActivity and off for the aliases.
        boolean isDefault = tier.isEmpty() == on;
        int want = isDefault ? PackageManager.COMPONENT_ENABLED_STATE_DEFAULT : on ? PackageManager.COMPONENT_ENABLED_STATE_ENABLED : PackageManager.COMPONENT_ENABLED_STATE_DISABLED;
        try {
            if (pm.getComponentEnabledSetting(cn) != want) pm.setComponentEnabledSetting(cn, want, PackageManager.DONT_KILL_APP);
        } catch (Exception ignored) {
        }
    }

    /** An intent that opens the app through whichever launcher entry is on. */
    static Intent openIntent(Context c) {
        Intent i = c.getPackageManager().getLaunchIntentForPackage(c.getPackageName());
        return i != null ? i : new Intent(c, MainActivity.class);
    }
}

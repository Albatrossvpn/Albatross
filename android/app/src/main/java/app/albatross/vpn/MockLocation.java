package app.albatross.vpn;

import android.content.Context;
import android.location.Criteria;
import android.location.Location;
import android.location.LocationManager;
import android.location.provider.ProviderProperties;
import android.os.Build;
import android.os.SystemClock;

import java.util.Collections;
import java.util.HashSet;
import java.util.Set;

/**
 * "Override GPS location": the phone reports a place in the VPN's country.
 * Android lets only the app chosen under Developer options, "Select mock
 * location app", set the location; until the user picks Albatross there,
 * Android refuses and this stays inactive. Main thread only.
 */
final class MockLocation {
    // The test providers added, which clear removes.
    private static final Set<String> added = new HashSet<>();
    private static double lat, lon;
    private static boolean have;
    // A test provider outlives the process that added it, so after a crash
    // the phone would stay in the VPN's country without its real GPS. The
    // first clear in a process removes every one Albatross may have added.
    private static boolean swept;

    private MockLocation() {
    }

    /** Puts the phone at lat, lon. */
    static void set(Context c, double lat, double lon) {
        MockLocation.lat = lat;
        MockLocation.lon = lon;
        have = true;
        push(c);
    }

    /** Reports the last location again with a fresh time, so it does not go stale. */
    static void keep(Context c) {
        if (have) push(c);
    }

    /** Gives the phone its real location back. */
    static void clear(Context c) {
        have = false;
        if (swept && added.isEmpty()) return;
        LocationManager lm = c.getSystemService(LocationManager.class);
        if (lm == null) return;
        if (!swept) {
            swept = true;
            Collections.addAll(added, providers());
        }
        for (String p : added) {
            try {
                lm.removeTestProvider(p);
            } catch (Exception ignored) {
            }
        }
        added.clear();
    }

    private static String[] providers() {
        if (Build.VERSION.SDK_INT >= 31) {
            return new String[]{LocationManager.GPS_PROVIDER, LocationManager.NETWORK_PROVIDER, LocationManager.FUSED_PROVIDER};
        }
        return new String[]{LocationManager.GPS_PROVIDER, LocationManager.NETWORK_PROVIDER};
    }

    private static void push(Context c) {
        LocationManager lm = c.getSystemService(LocationManager.class);
        if (lm == null) return;
        for (String p : providers()) {
            try {
                if (added.add(p)) {
                    try {
                        addTestProvider(lm, p);
                    } catch (IllegalArgumentException e) {
                        // Older Android: still a test provider from before.
                    }
                    lm.setTestProviderEnabled(p, true);
                }
                lm.setTestProviderLocation(p, location(p));
            } catch (SecurityException e) {
                // Albatross is not the mock location app.
                added.remove(p);
                return;
            } catch (IllegalArgumentException e) {
                // Not a test provider: Android ignores adding one while
                // Albatross is not the mock location app. Add it next time.
                added.remove(p);
            }
        }
    }

    @SuppressWarnings("deprecation")
    private static void addTestProvider(LocationManager lm, String provider) {
        if (Build.VERSION.SDK_INT >= 31) {
            lm.addTestProvider(provider, new ProviderProperties.Builder()
                    .setHasAltitudeSupport(true)
                    .setHasSpeedSupport(true)
                    .setHasBearingSupport(true)
                    .setAccuracy(ProviderProperties.ACCURACY_FINE)
                    .setPowerUsage(ProviderProperties.POWER_USAGE_LOW)
                    .build());
        } else {
            lm.addTestProvider(provider, false, false, false, false, true, true, true, Criteria.POWER_LOW, Criteria.ACCURACY_FINE);
        }
    }

    private static Location location(String provider) {
        Location l = new Location(provider);
        l.setLatitude(lat);
        l.setLongitude(lon);
        l.setAccuracy(10f);
        l.setAltitude(30);
        l.setSpeed(0f);
        l.setTime(System.currentTimeMillis());
        l.setElapsedRealtimeNanos(SystemClock.elapsedRealtimeNanos());
        if (Build.VERSION.SDK_INT >= 26) {
            l.setVerticalAccuracyMeters(15f);
            l.setSpeedAccuracyMetersPerSecond(0.5f);
            l.setBearingAccuracyDegrees(30f);
        }
        return l;
    }
}

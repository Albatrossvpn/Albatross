package app.albatross.vpn;

import android.app.Notification;
import android.app.PendingIntent;
import android.content.Context;
import android.content.Intent;
import android.content.pm.PackageManager;
import android.content.pm.ServiceInfo;
import android.net.VpnService;
import android.os.Build;
import android.os.ParcelFileDescriptor;
import android.util.Log;

import app.albatross.mobile.Mobile;

/**
 * The device-wide VPN. Every app's traffic enters this tunnel; the Go side
 * hands it to the engine's local proxy, which sends it through the servers.
 * Albatross itself is left out, so the cores reach the servers directly.
 */
public final class TunnelService extends VpnService {
    private static final String TAG = "Albatross";
    private static final String ACTION_STOP = "app.albatross.vpn.STOP";
    private static final String ACTION_DISCONNECT = "app.albatross.vpn.DISCONNECT";

    private ParcelFileDescriptor tun;

    static void start(Context c) {
        Intent i = new Intent(c, TunnelService.class);
        if (Build.VERSION.SDK_INT >= 26) c.startForegroundService(i);
        else c.startService(i);
    }

    static void stop(Context c) {
        try {
            c.startService(new Intent(c, TunnelService.class).setAction(ACTION_STOP));
        } catch (IllegalStateException e) {
            // In the background Android refuses to start a service that is
            // not running, and then there is nothing to stop.
        }
    }

    @Override
    public int onStartCommand(Intent intent, int flags, int startId) {
        String action = intent == null ? null : intent.getAction();
        if (ACTION_STOP.equals(action)) {
            close();
            stopSelf();
            return START_NOT_STICKY;
        }
        if (ACTION_DISCONNECT.equals(action)) {
            // The engine clears its "system proxy", which stops this service.
            new Thread(Mobile::disconnect).start();
            return START_NOT_STICKY;
        }
        foreground();
        if (tun == null && SERVICE_INTERFACE.equals(action)) {
            // Android started the VPN by itself (Always-on VPN, for example
            // after a restart). Connect again if the user left it on; the
            // engine opens the tunnel once a server is ready.
            new Thread(() -> {
                boolean resumed = false;
                try {
                    ((App) getApplication()).engine();
                    resumed = Mobile.resume();
                } catch (Exception e) {
                    Log.e(TAG, "starting the engine failed", e);
                }
                if (!resumed) stop(this);
            }).start();
            return START_NOT_STICKY;
        }
        if (tun == null && !open()) {
            close();
            stopSelf();
            new Thread(Mobile::disconnect).start();
        }
        return START_NOT_STICKY;
    }

    private boolean open() {
        try {
            tun = builder(true).establish();
        } catch (Exception e) {
            // A device that refuses one of the local-network exclusions
            // still gets a working tunnel, only without them.
            Log.e(TAG, "the tunnel refused the local-network routes", e);
            try {
                tun = builder(false).establish();
            } catch (Exception e2) {
                Log.e(TAG, "starting the tunnel failed", e2);
                return false;
            }
        }
        if (tun == null) return false; // permission was revoked
        try {
            Mobile.startTun(tun.getFd());
            return true;
        } catch (Exception e) {
            Log.e(TAG, "starting the tunnel failed", e);
            return false;
        }
    }

    private Builder builder(boolean lan) {
        Builder b = new Builder()
                .setSession(getString(R.string.app_name))
                .setMtu((int) Mobile.MTU)
                .addAddress("172.19.0.1", 30)
                .addRoute("0.0.0.0", 0)
                .addAddress("fdfe:dcba:9876::1", 126)
                .addRoute("::", 0)
                // Answered by the engine (DNS through the VPN).
                .addDnsServer("172.19.0.2");
        try {
            b.addDisallowedApplication(getPackageName());
        } catch (PackageManager.NameNotFoundException ignored) {
        }
        // The country's own apps (banks, payments, taxis, shops) refuse a
        // foreign address or stop when a VPN is on: they keep their direct
        // line ("Local apps outside the VPN").
        String prefixes = Mobile.bypassApps();
        if (!prefixes.isEmpty()) {
            String[] ps = prefixes.split(",");
            for (android.content.pm.ApplicationInfo ai : getPackageManager().getInstalledApplications(0)) {
                if (ai.packageName.equals(getPackageName())) continue;
                for (String p : ps) {
                    if (ai.packageName.startsWith(p)) {
                        try {
                            b.addDisallowedApplication(ai.packageName);
                        } catch (PackageManager.NameNotFoundException ignored) {
                        }
                        break;
                    }
                }
            }
        }
        if (lan && Build.VERSION.SDK_INT >= 33) {
            // The local network stays outside the tunnel, so casting to a TV
            // and finding devices (mDNS, SSDP) keep working.
            for (String net : Mobile.lanNets().split(",")) {
                String[] p = net.split("/");
                try {
                    b.excludeRoute(new android.net.IpPrefix(java.net.InetAddress.getByName(p[0]), Integer.parseInt(p[1])));
                } catch (Exception ignored) {
                }
            }
        }
        if (Build.VERSION.SDK_INT >= 29) b.setMetered(false);
        b.setConfigureIntent(PendingIntent.getActivity(this, 0, new Intent(this, MainActivity.class), PendingIntent.FLAG_IMMUTABLE));
        return b;
    }

    // The notification follows the engine: "Connecting" while it searches
    // or reconnects, "Connected" once traffic flows through a server. So
    // does the phone's location while "Override GPS location" is on.
    private final android.os.Handler ticker = new android.os.Handler(android.os.Looper.getMainLooper());
    private Boolean shown;
    private final Runnable tick = new Runnable() {
        @Override
        public void run() {
            boolean connected = Mobile.connected();
            if (shown == null || shown != connected) {
                shown = connected;
                getSystemService(android.app.NotificationManager.class).notify(1, notification(connected));
            }
            mockLocation();
            ticker.postDelayed(this, 2000);
        }
    };

    private void mockLocation() {
        String at = Mobile.mockLocation();
        if (at.isEmpty()) {
            MockLocation.clear(this);
            return;
        }
        if (at.equals("keep")) {
            MockLocation.keep(this);
            return;
        }
        String[] p = at.split(",");
        double lat, lon;
        try {
            lat = Double.parseDouble(p[0]);
            lon = Double.parseDouble(p[1]);
        } catch (RuntimeException e) {
            return;
        }
        MockLocation.set(this, lat, lon);
    }

    private void foreground() {
        shown = false;
        Notification n = notification(false);
        if (Build.VERSION.SDK_INT >= 34) startForeground(1, n, ServiceInfo.FOREGROUND_SERVICE_TYPE_SPECIAL_USE);
        else startForeground(1, n);
        ticker.removeCallbacks(tick);
        ticker.postDelayed(tick, 1000);
    }

    private Notification notification(boolean connected) {
        PendingIntent open = PendingIntent.getActivity(this, 0, new Intent(this, MainActivity.class), PendingIntent.FLAG_IMMUTABLE);
        PendingIntent disconnect = PendingIntent.getService(this, 1,
                new Intent(this, TunnelService.class).setAction(ACTION_DISCONNECT), PendingIntent.FLAG_IMMUTABLE);
        Notification.Builder nb = Build.VERSION.SDK_INT >= 26 ? new Notification.Builder(this, App.CHANNEL) : new Notification.Builder(this);
        return nb.setSmallIcon(R.drawable.ic_stat)
                .setContentTitle(getString(connected ? R.string.connected : R.string.connecting))
                .setContentText(getString(connected ? R.string.connected_text : R.string.connecting_text))
                .setContentIntent(open)
                .setOngoing(true)
                .setOnlyAlertOnce(true)
                .addAction(new Notification.Action.Builder(null, getString(R.string.disconnect), disconnect).build())
                .build();
    }

    private void close() {
        ticker.removeCallbacks(tick);
        Mobile.stopTun();
        if (tun != null) {
            try {
                tun.close();
            } catch (Exception ignored) {
            }
            tun = null;
        }
        MockLocation.clear(this);
        stopForeground(true);
    }

    @Override
    public void onRevoke() {
        // Another VPN took over, or the user switched this one off in the
        // system settings.
        close();
        new Thread(Mobile::disconnect).start();
        stopSelf();
    }

    @Override
    public void onDestroy() {
        close();
        super.onDestroy();
    }
}

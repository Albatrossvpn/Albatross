package app.skyroad.vpn;

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

import app.skyroad.mobile.Mobile;

/**
 * The device-wide VPN. Every app's traffic enters this tunnel; the Go side
 * hands it to the engine's local proxy, which sends it through the servers.
 * Skyroad itself is left out, so the cores reach the servers directly.
 */
public final class TunnelService extends VpnService {
    private static final String TAG = "Skyroad";
    private static final String ACTION_STOP = "app.skyroad.vpn.STOP";
    private static final String ACTION_DISCONNECT = "app.skyroad.vpn.DISCONNECT";

    private ParcelFileDescriptor tun;

    static void start(Context c) {
        Intent i = new Intent(c, TunnelService.class);
        if (Build.VERSION.SDK_INT >= 26) c.startForegroundService(i);
        else c.startService(i);
    }

    static void stop(Context c) {
        c.startService(new Intent(c, TunnelService.class).setAction(ACTION_STOP));
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
        if (tun == null && !open()) {
            close();
            stopSelf();
            new Thread(Mobile::disconnect).start();
        }
        return START_NOT_STICKY;
    }

    private boolean open() {
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
        if (Build.VERSION.SDK_INT >= 29) b.setMetered(false);
        b.setConfigureIntent(PendingIntent.getActivity(this, 0, new Intent(this, MainActivity.class), PendingIntent.FLAG_IMMUTABLE));
        try {
            tun = b.establish();
            if (tun == null) return false; // permission was revoked
            Mobile.startTun(tun.getFd());
            return true;
        } catch (Exception e) {
            Log.e(TAG, "starting the tunnel failed", e);
            return false;
        }
    }

    private void foreground() {
        PendingIntent open = PendingIntent.getActivity(this, 0, new Intent(this, MainActivity.class), PendingIntent.FLAG_IMMUTABLE);
        PendingIntent disconnect = PendingIntent.getService(this, 1,
                new Intent(this, TunnelService.class).setAction(ACTION_DISCONNECT), PendingIntent.FLAG_IMMUTABLE);
        Notification.Builder nb = Build.VERSION.SDK_INT >= 26 ? new Notification.Builder(this, App.CHANNEL) : new Notification.Builder(this);
        Notification n = nb.setSmallIcon(R.drawable.ic_stat)
                .setContentTitle(getString(R.string.connected))
                .setContentText(getString(R.string.connected_text))
                .setContentIntent(open)
                .setOngoing(true)
                .addAction(new Notification.Action.Builder(null, getString(R.string.disconnect), disconnect).build())
                .build();
        if (Build.VERSION.SDK_INT >= 34) startForeground(1, n, ServiceInfo.FOREGROUND_SERVICE_TYPE_SPECIAL_USE);
        else startForeground(1, n);
    }

    private void close() {
        Mobile.stopTun();
        if (tun != null) {
            try {
                tun.close();
            } catch (Exception ignored) {
            }
            tun = null;
        }
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

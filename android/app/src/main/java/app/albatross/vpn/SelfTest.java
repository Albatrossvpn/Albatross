package app.albatross.vpn;

import android.content.Intent;
import android.content.pm.ApplicationInfo;
import android.util.Log;

import java.io.ByteArrayOutputStream;
import java.io.InputStream;
import java.io.OutputStream;
import java.net.HttpURLConnection;
import java.net.URL;
import java.nio.charset.StandardCharsets;

/**
 * Connects and reports progress to logcat, for CI's emulator test. Runs
 * only in debuggable builds, never in the ones users get.
 */
final class SelfTest {
    static final String ACTION = "app.albatross.vpn.SELF_TEST";
    private static final String TAG = "AlbatrossTest";

    static void maybeRun(MainActivity a, Intent intent) {
        if (intent == null || !ACTION.equals(intent.getAction())) return;
        if ((a.getApplicationInfo().flags & ApplicationInfo.FLAG_DEBUGGABLE) == 0) return;
        App app = (App) a.getApplication();
        new Thread(() -> {
            try {
                String url = app.engine();
                int hash = url.indexOf('#');
                String base = url.substring(0, url.indexOf('/', "http://".length()));
                String token = url.substring(hash + 1);
                Log.i(TAG, "connect: " + call(base, token, "POST", "/api/connect", "{}"));
                int shown = 0;
                for (int i = 0; i < 120; i++) {
                    Thread.sleep(5000);
                    String state = call(base, token, "GET", "/api/state", null);
                    Log.i(TAG, "state: " + state.substring(0, Math.min(state.length(), 600)));
                    String logs = call(base, token, "GET", "/api/logs", null);
                    if (logs.length() > shown) {
                        Log.i(TAG, "logs: " + logs.substring(shown, Math.min(logs.length(), shown + 3000)));
                        shown = logs.length();
                    }
                }
                Log.i(TAG, "done");
            } catch (Exception e) {
                Log.e(TAG, "failed", e);
            }
        }).start();
    }

    private static String call(String base, String token, String method, String path, String body) throws Exception {
        HttpURLConnection c = (HttpURLConnection) new URL(base + path).openConnection();
        c.setRequestMethod(method);
        c.setRequestProperty("X-Token", token);
        c.setConnectTimeout(5000);
        c.setReadTimeout(15000);
        if (body != null) {
            c.setDoOutput(true);
            c.setRequestProperty("Content-Type", "application/json");
            try (OutputStream o = c.getOutputStream()) {
                o.write(body.getBytes(StandardCharsets.UTF_8));
            }
        }
        int code = c.getResponseCode();
        InputStream in = code < 400 ? c.getInputStream() : c.getErrorStream();
        ByteArrayOutputStream out = new ByteArrayOutputStream();
        if (in != null) {
            byte[] buf = new byte[8192];
            for (int n; (n = in.read(buf)) > 0; ) out.write(buf, 0, n);
            in.close();
        }
        return code + " " + out.toString("UTF-8");
    }
}

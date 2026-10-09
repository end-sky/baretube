#include <gtk/gtk.h>
#include <stdlib.h>
#include "home.h"

int main(int argc, char **argv) {
    static AppState app;
    gtk_init(&argc, &argv);

    config_load(&app.config);
    app.backend_path = g_strdup(g_getenv("BARETUBE_BACKEND") ? g_getenv("BARETUBE_BACKEND") : "baretube-backend");
    app.mpv_path = g_strdup(g_getenv("BARETUBE_MPV") ? g_getenv("BARETUBE_MPV") : "mpv");
    app.window = gtk_window_new(GTK_WINDOW_TOPLEVEL);
    gtk_window_set_title(GTK_WINDOW(app.window), "BareTube");
    gtk_window_set_default_size(GTK_WINDOW(app.window), 850, 620);
    gtk_window_set_position(GTK_WINDOW(app.window), GTK_WIN_POS_CENTER);
    g_signal_connect(app.window, "destroy", G_CALLBACK(gtk_main_quit), NULL);

    GtkWidget *root = home_build(&app);
    gtk_container_add(GTK_CONTAINER(app.window), root);
    config_apply_theme(app.window, app.config.theme);
    gtk_widget_show_all(app.window);
    gtk_main();

    return 0;
}

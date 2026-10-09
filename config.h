#ifndef BARETUBE_CONFIG_H
#define BARETUBE_CONFIG_H

#include <gtk/gtk.h>

typedef struct {
    char backend[24];   /* local or invidious */
    char instance[256]; /* Invidious instance URL */
    char theme[16];     /* amoled or white */
} AppConfig;

void config_load(AppConfig *config);
void config_save(const AppConfig *config);
void config_apply_theme(GtkWidget *widget, const char *theme);

#endif

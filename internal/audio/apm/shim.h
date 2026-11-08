// C interface to the WebRTC audio processing module (shim.cc), for cgo.
#ifndef MURMEL_APM_SHIM_H
#define MURMEL_APM_SHIM_H

#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

typedef struct murmel_apm murmel_apm;

// murmel_apm_new creates a processor for 48 kHz mono, 10 ms frames
// (NULL on failure).
murmel_apm *murmel_apm_new(void);
void murmel_apm_free(murmel_apm *apm);

// murmel_apm_configure applies a configuration. noise_level: -1 off,
// 0..3 = low, moderate, high, very high. Booleans are 0/1.
void murmel_apm_configure(murmel_apm *apm, int noise_level, int high_pass, int echo_cancel);

// murmel_apm_process processes one 480-sample capture frame in place;
// returns 0 on success (a WebRTC error code otherwise).
int murmel_apm_process(murmel_apm *apm, int16_t *frame);

// murmel_apm_process_reverse feeds one 480-sample frame of the audio
// being played back (echo cancellation reference); returns 0 on success.
int murmel_apm_process_reverse(murmel_apm *apm, int16_t *frame);

// murmel_apm_set_delay tells the echo canceller how far the capture lags
// the playback reference, in ms.
int murmel_apm_set_delay(murmel_apm *apm, int delay_ms);

#ifdef __cplusplus
}
#endif

#endif

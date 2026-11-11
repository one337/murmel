{
  lib,
  buildGoModule,
  go_1_27,
  pkg-config,
  wrapGAppsHook4,
  gettext,
  desktop-file-utils,
  glib,
  gtk4,
  libadwaita,
  libopus,
  webrtc-audio-processing,
  libsecret,
  graphene,
  gobject-introspection,
  libpulseaudio,
  src,
  version,
}:

(buildGoModule.override { go = go_1_27; }) {
  pname = "murmel";
  inherit version src;

  # The Go dependencies are vendored in the repository.
  vendorHash = null;
  subPackages = [ "cmd/murmel" ];
  ldflags = [ "-X io.github/murmel/internal/ui.Version=${version}" ];

  nativeBuildInputs = [
    pkg-config
    wrapGAppsHook4
    gettext
    desktop-file-utils
    glib
  ];

  buildInputs = [
    gtk4
    libadwaita
    libopus
    webrtc-audio-processing
    libsecret
    graphene
    gobject-introspection
  ];

  # The desktop integration (schema, desktop file, icons, D-Bus service,
  # translations, licenses) is installed by tools/install.sh, as for the
  # Flatpak and the RPM.
  postInstall = ''
    patchShebangs tools
    cp $out/bin/murmel murmel
    tools/install.sh --no-build --prefix $out
  '';

  # miniaudio opens the sound server's client library at runtime (dlopen);
  # NixOS has no global library path, so the wrapper has to name it.
  preFixup = ''
    gappsWrapperArgs+=(--prefix LD_LIBRARY_PATH : ${lib.makeLibraryPath [ libpulseaudio ]})
  '';

  meta = {
    description = "Mumble voice chat client for GNOME";
    homepage = "https://simon-tschoep.de/software/murmel/";
    license = with lib.licenses; [
      gpl3Plus
      mpl20
      bsd3
      mit
      unlicense
      cc0
    ];
    mainProgram = "murmel";
    platforms = lib.platforms.linux;
  };
}

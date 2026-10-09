{ pkgs ? import <nixpkgs> {} }:
pkgs.callPackage ({ stdenv, go, pkg-config, gtk3, mpv, lib }:
  stdenv.mkDerivation {
    pname = "baretube";
    version = "0.1.0";
    src = ./.;
    nativeBuildInputs = [ go pkg-config ];
    buildInputs = [ gtk3 ];
    dontConfigure = true;
    buildPhase = ''
      # Nix builders have no writable home directory. Keep all Go caches
      # in the temporary build directory instead of /homeless-shelter.
      export HOME="$TMPDIR/home"
      export GOCACHE="$TMPDIR/go-build-cache"
      export GOPATH="$TMPDIR/go"
      export GOMODCACHE="$GOPATH/pkg/mod"
      mkdir -p "$HOME" "$GOCACHE" "$GOMODCACHE"
      export CGO_ENABLED=0
      go build -trimpath -ldflags='-s -w -buildid=' -o baretube-backend main.go scrape.go
      $CC -std=c11 -O2 -DNDEBUG -s -Wall -Wextra $(pkg-config --cflags gtk+-3.0) -o baretube-bin main.c home.c config.c $(pkg-config --libs gtk+-3.0)
    '';
    installPhase = ''
      mkdir -p $out/bin
      install -m755 baretube-bin $out/bin/baretube-bin
      install -m755 baretube-backend $out/bin/baretube-backend
      cat > $out/bin/baretube <<EOF_WRAPPER
      #!${stdenv.shell}
      export BARETUBE_BACKEND="$out/bin/baretube-backend"
      export BARETUBE_MPV="${mpv}/bin/mpv"
      exec "$out/bin/baretube-bin" "\$@"
      EOF_WRAPPER
      chmod 755 $out/bin/baretube
    '';
    meta = { description = "Minimal C/Go YouTube client"; license = lib.licenses.agpl3Plus; platforms = lib.platforms.linux; mainProgram = "baretube"; };
  }) {}

{
  description = "BareTube - a tiny C/Go YouTube client";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

  outputs = { self, nixpkgs }:
    let
      systems = [ "x86_64-linux" "aarch64-linux" ];
      forAllSystems = nixpkgs.lib.genAttrs systems;
    in {
      packages = forAllSystems (system:
        let
          pkgs = import nixpkgs { inherit system; };
        in {
          default = pkgs.stdenv.mkDerivation {
            pname = "baretube";
            version = "0.1.0";
            src = self;
            nativeBuildInputs = [ pkgs.go pkgs.pkg-config ];
            buildInputs = [ pkgs.gtk3 ];
            dontConfigure = true;
            buildPhase = ''
              runHook preBuild
              # Nix builders have no writable home directory. Keep all Go caches
              # in the temporary build directory instead of /homeless-shelter.
              export HOME="$TMPDIR/home"
              export GOCACHE="$TMPDIR/go-build-cache"
              export GOPATH="$TMPDIR/go"
              export GOMODCACHE="$GOPATH/pkg/mod"
              mkdir -p "$HOME" "$GOCACHE" "$GOMODCACHE"
              export CGO_ENABLED=0
              go build -trimpath -ldflags='-s -w -buildid=' -o baretube-backend main.go scrape.go
              $CC -std=c11 -O2 -DNDEBUG -s -Wall -Wextra \
                $(pkg-config --cflags gtk+-3.0) \
                -o baretube-bin main.c home.c config.c \
                $(pkg-config --libs gtk+-3.0)
              runHook postBuild
            '';
            installPhase = ''
              runHook preInstall
              mkdir -p $out/bin $out/share/applications
              install -m755 baretube-bin $out/bin/baretube-bin
              install -m755 baretube-backend $out/bin/baretube-backend
              cat > $out/bin/baretube <<EOF_WRAPPER
              #!${pkgs.runtimeShell}
              export BARETUBE_BACKEND="$out/bin/baretube-backend"
              export BARETUBE_MPV="${pkgs.mpv}/bin/mpv"
              exec "$out/bin/baretube-bin" "\$@"
              EOF_WRAPPER
              chmod 755 $out/bin/baretube
              cat > $out/share/applications/baretube.desktop <<EOF_DESKTOP
              [Desktop Entry]
              Type=Application
              Name=BareTube
              Comment=Minimal YouTube search and playback client
              Exec=$out/bin/baretube
              Terminal=false
              Categories=AudioVideo;Player;Network;
              EOF_DESKTOP
              runHook postInstall
            '';
            meta = with pkgs.lib; {
              description = "A minimal YouTube client with a C GUI and Go scraper";
              license = licenses.agpl3Plus;
              platforms = platforms.linux;
              mainProgram = "baretube";
            };
          };
        });
      apps = forAllSystems (system: {
        default = {
          type = "app";
          program = "${self.packages.${system}.default}/bin/baretube";
        };
      });
    };
}

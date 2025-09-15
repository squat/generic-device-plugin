{
  description = "The generic-device-plugin enables allocating generic Linux devices, such as serial devices, the FUSE device, or video cameras, to Kubernetes Pods";

  inputs = {
    nixpkgs.url = "github:nixos/nixpkgs/nixos-unstable";
    flake-parts.url = "github:hercules-ci/flake-parts";
    git-hooks-nix = {
      url = "github:cachix/git-hooks.nix";
      inputs.nixpkgs.follows = "nixpkgs";
    };
  };

  outputs =
    inputs:
    inputs.flake-parts.lib.mkFlake { inherit inputs; } {
      imports = [
        inputs.git-hooks-nix.flakeModule
      ];
      systems = [
        "x86_64-linux"
        "aarch64-linux"
        "aarch64-darwin"
      ];
      perSystem =
        {
          pkgs,
          system,
          config,
          ...
        }:
        {
          packages = rec {
            generic-device-plugin = pkgs.buildGoModule rec {
              pname = "generic-device-plugin";
              version = "0.0.1";
              src = ./.;
              vendorHash = null;
              checkFlags = [ "-skip=^TestE2E" ];
              env.CGO_ENABLED = 0;
              ldflags = [
                "-s -w -X github.com/squat/generic-device-plugin/version.Version=${version}"
              ];

              meta = {
                description = "The generic-device-plugin enables allocating generic Linux devices, such as serial devices, the FUSE device, or video cameras, to Kubernetes Pods";
                mainProgram = "generic-device-plugin";
                homepage = "https://github.com/squat/generic-device-plugin";
              };
            };

            generic-device-plugin-cross-linux-amd64 = generic-device-plugin.overrideAttrs {
              env.GOOS = "linux";
              env.GOARCH = "amd64";
              env.CGO_ENABLED = 0;
              checkPhase = false;
            };

            generic-device-plugin-cross-linux-arm = generic-device-plugin.overrideAttrs {
              env.GOOS = "linux";
              env.GOARCH = "arm";
              env.CGO_ENABLED = 0;
              checkPhase = false;
            };

            generic-device-plugin-cross-linux-arm64 = generic-device-plugin.overrideAttrs {
              env.GOOS = "linux";
              env.GOARCH = "arm64";
              env.CGO_ENABLED = 0;
              checkPhase = false;
            };

            default = generic-device-plugin;
          };

          pre-commit = {
            check.enable = true;
            settings = {
              src = ./.;
              hooks = {
                actionlint.enable = true;
                nixfmt-rfc-style.enable = true;
                nixfmt-rfc-style.excludes = [ "vendor" ];
                gofmt.enable = true;
                gofmt.excludes = [ "vendor" ];
                golangci-lint.enable = true;
                golangci-lint.excludes = [ "vendor" ];
                golangci-lint.extraPackages = [ pkgs.go ];
                govet.enable = true;
                govet.excludes = [ "vendor" ];
                yamlfmt.enable = true;
                yamlfmt.args = [
                  "--formatter"
                  "indentless_arrays=true"
                ];
                yamlfmt.excludes = [
                  ".github"
                  "vendor"
                ];
                header = {
                  enable = true;
                  name = "Header";
                  entry =
                    let
                      headerCheck = pkgs.writeShellApplication {
                        name = "header-check";
                        text = ''
                          log() { 
                            local ln="$LINENO" bc="$BASH_COMMAND" ec="$?"
                            printf "encountered error while executing '%s' on line %d\n" "$bc" "$ln"
                            exit "$ec"
                          }
                          trap log ERR
                          HEADER=$(cat ${./.header})
                          HEADER_LEN=$(wc -l ${./.header} | awk '{print $1}')
                          FILES=
                          for f in "$@"; do 
                              for i in 0 1 2 3 4 5; do 
                                  FILE=$(tail -n +$i "$f" | head -n "$HEADER_LEN" | sed "s/[0-9]\{4\}/YEAR/")
                                  [ "$FILE" = "$HEADER" ] && continue 2
                              done
                              FILES="$FILES$f "
                          done
                          if [ -n "$FILES" ]; then \
                              printf 'the following files are missing the license header: %s\n' "$FILES"; \
                              exit 1
                          fi
                        '';
                      };
                    in
                    pkgs.lib.getExe headerCheck;
                  files = "\\.(go)$";
                  excludes = [ "vendor" ];
                };
                readme = {
                  enable = true;
                  name = "README.md";
                  entry =
                    let
                      readmeCheck = pkgs.writeShellApplication {
                        name = "readme-check";
                        text = ''
                          (go run ./... --help 2>&1 1>/dev/null || [ $? -eq 1 ]) | sed 's/\(Usage of\).*\(generic-device-plugin:\)/\1 \2/' > help.txt
                          go tool embedmd -d README.md
                        '';
                      };
                    in
                    pkgs.lib.getExe readmeCheck;
                  files = "^README\\.md$";
                  extraPackages = [ pkgs.go ];
                };
              };
            };
          };

          devShells = {
            default = pkgs.mkShell {
              inherit (config.pre-commit.devShell) shellHook;
              packages =
                with pkgs;
                [
                  go
                  kind
                  kubectl
                ]
                ++ config.pre-commit.settings.enabledPackages;
            };
          };
        };
    };
}

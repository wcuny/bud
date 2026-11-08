%global debug_package %{nil}
%global _build_id_links none

Name:           bud
Version:        %{?bud_version}%{!?bud_version:0.1.0}
Release:        1%{?dist}
Summary:        Library of commands
License:        Proprietary
Source0:        %{name}-%{version}.tar.gz

BuildRequires:  golang >= 1.22
BuildRequires:  make

%description
bud keeps a library of commands with names, tips, tags and
directory/repo scoping.

%prep
%autosetup

%build
make build

%install
make install DESTDIR=%{buildroot} PREFIX=%{_prefix}

%files
%{_bindir}/bud
%{_datadir}/bash-completion/completions/bud
%{_datadir}/zsh/site-functions/_bud
%{_datadir}/fish/vendor_completions.d/bud.fish
%doc README.md

%changelog
* Sun Nov 08 2026 Will Cuny - 0.1.0-1
- Initial package

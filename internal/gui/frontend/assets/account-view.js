export function resolveAccountProviderView(providers, requestedProvider) {
  const list = Array.isArray(providers) ? providers : [];
  const requested = String(requestedProvider || "");
  const selected = requested && list.some(function (provider) {
    return provider && provider.id === requested;
  }) ? requested : "";
  const visible = selected
    ? list.filter(function (provider) {
      return provider && provider.id === selected;
    })
    : list.filter(function (provider) {
      return provider && Array.isArray(provider.accounts) &&
        provider.accounts.length > 0;
    });
  return {
    selectedProvider: selected,
    providers: visible,
  };
}

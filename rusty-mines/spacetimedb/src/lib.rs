mod policy;

// Reducers/table exports require the SpacetimeDB WASM host. Native unit tests
// exercise the exact pure policy functions used by those reducers instead.
#[cfg(not(test))]
mod foundation;
#[cfg(not(test))]
mod inventory;
#[cfg(not(test))]
mod world;

import { useEffect, useState } from "react";
import { api, Unauthorized } from "./api";
import { Dashboard } from "./Dashboard";
import { Login } from "./Login";

type Auth = { state: "checking" } | { state: "out" } | { state: "in"; username: string };

export function App() {
  const [auth, setAuth] = useState<Auth>({ state: "checking" });

  useEffect(() => {
    api
      .me()
      .then((u) => setAuth({ state: "in", username: u.username }))
      .catch((e) => {
        if (!(e instanceof Unauthorized)) console.error(e);
        setAuth({ state: "out" });
      });
  }, []);

  if (auth.state === "checking") return <div className="splash">Checking session…</div>;
  if (auth.state === "out") {
    return <Login onSignedIn={(username) => setAuth({ state: "in", username })} />;
  }
  return (
    <Dashboard
      username={auth.username}
      onSignedOut={() => setAuth({ state: "out" })}
    />
  );
}

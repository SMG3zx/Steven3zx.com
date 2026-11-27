"""Path joining for the folders vinci reads and writes."""


def join_path(directory: str, name: str) -> str:
    """Path.Combine for a relative name. An empty directory gives the name alone."""
    if directory == "":
        return name
    if directory.endswith("/"):
        return directory + name
    return directory + "/" + name

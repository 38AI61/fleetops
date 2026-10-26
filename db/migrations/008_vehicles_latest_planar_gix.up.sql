CREATE INDEX vehicles_latest_geom_planar_gix ON vehicles_latest USING GIST ((geom::geometry));
